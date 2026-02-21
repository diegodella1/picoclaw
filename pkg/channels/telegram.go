package channels

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/utils"
	"github.com/sipeed/picoclaw/pkg/voice"
)

// Pre-compiled regex patterns (avoid re-compiling on every message)
var (
	reCodeBlock    = regexp.MustCompile("```[\\w]*\\n?[\\s\\S]*?```")
	reInlineCode   = regexp.MustCompile("`[^`]+`")
	reBold         = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reUnderBold    = regexp.MustCompile(`__(.+?)__`)
	reItalic       = regexp.MustCompile(`_([^_]+)_`)
	reStrike       = regexp.MustCompile(`~~(.+?)~~`)
	reLink         = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	reHeading      = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	reBlockquote   = regexp.MustCompile(`(?m)^>\s*(.*)$`)
	reListItem     = regexp.MustCompile(`(?m)^[-*]\s+`)
	reCodeBlockCap = regexp.MustCompile("```[\\w]*\\n?([\\s\\S]*?)```")
)

type TelegramChannel struct {
	*BaseChannel
	bot          *telego.Bot
	config       config.TelegramConfig
	appConfig    *config.Config
	chatIDs      map[string]int64
	transcriber  *voice.GroqTranscriber
	placeholders sync.Map // chatID -> messageID
	stopThinking sync.Map // chatID -> thinkingCancel
	voiceInput   sync.Map // chatID -> bool (true if last input was voice/audio)
}

var defaultModels = []string{
	// Codex models (ChatGPT Plus via OAuth)
	"gpt-5.2-codex",
	"gpt-5.3-codex",
	"gpt-5-codex",
	"gpt-5.1-codex-max",
	"gpt-5",
}

type thinkingCancel struct {
	fn context.CancelFunc
}

func (c *thinkingCancel) Cancel() {
	if c != nil && c.fn != nil {
		c.fn()
	}
}

func NewTelegramChannel(cfg config.TelegramConfig, bus *bus.MessageBus, appConfig *config.Config) (*TelegramChannel, error) {
	var opts []telego.BotOption

	if cfg.Proxy != "" {
		proxyURL, parseErr := url.Parse(cfg.Proxy)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid proxy URL %q: %w", cfg.Proxy, parseErr)
		}
		opts = append(opts, telego.WithHTTPClient(&http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
			},
		}))
	}

	bot, err := telego.NewBot(cfg.Token, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create telegram bot: %w", err)
	}

	base := NewBaseChannel("telegram", cfg, bus, cfg.AllowFrom)

	return &TelegramChannel{
		BaseChannel:  base,
		bot:          bot,
		config:       cfg,
		appConfig:    appConfig,
		chatIDs:      make(map[string]int64),
		transcriber:  nil,
		placeholders: sync.Map{},
		stopThinking: sync.Map{},
	}, nil
}

func (c *TelegramChannel) SetTranscriber(transcriber *voice.GroqTranscriber) {
	c.transcriber = transcriber
}

func (c *TelegramChannel) Start(ctx context.Context) error {
	logger.InfoC("telegram", "Starting Telegram bot (polling mode)...")

	if err := c.startPolling(ctx); err != nil {
		return err
	}

	return nil
}

func (c *TelegramChannel) startPolling(ctx context.Context) error {
	updates, err := c.bot.UpdatesViaLongPolling(ctx, &telego.GetUpdatesParams{
		Timeout:        30,
		AllowedUpdates: []string{"message", "callback_query"},
	})
	if err != nil {
		return fmt.Errorf("failed to start long polling: %w", err)
	}

	c.setRunning(true)
	logger.InfoCF("telegram", "Telegram bot connected", map[string]interface{}{
		"username": c.bot.Username(),
	})

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case update, ok := <-updates:
				if !ok {
					logger.WarnC("telegram", "Updates channel closed, attempting reconnect...")
					c.setRunning(false)
					c.reconnectPolling(ctx)
					return
				}
				if update.CallbackQuery != nil {
					c.handleCallbackQuery(ctx, update)
				} else if update.Message != nil {
					c.handleMessage(ctx, update)
				}
			}
		}
	}()

	return nil
}

func (c *TelegramChannel) reconnectPolling(ctx context.Context) {
	backoff := 2 * time.Second
	maxBackoff := 5 * time.Minute

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		logger.InfoCF("telegram", "Reconnecting polling", map[string]interface{}{
			"backoff": backoff.String(),
		})

		if err := c.startPolling(ctx); err != nil {
			logger.ErrorCF("telegram", "Reconnect failed", map[string]interface{}{
				"error": err.Error(),
			})
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		return
	}
}

func (c *TelegramChannel) Stop(ctx context.Context) error {
	logger.InfoC("telegram", "Stopping Telegram bot...")
	c.setRunning(false)
	return nil
}

// ttsMaxChars is the max plain-text length for a response to be sent as voice.
const ttsMaxChars = 300

func (c *TelegramChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if !c.IsRunning() {
		return fmt.Errorf("telegram bot not running")
	}

	chatID, err := parseChatID(msg.ChatID)
	if err != nil {
		return fmt.Errorf("invalid chat ID: %w", err)
	}

	// Stop thinking animation
	if stop, ok := c.stopThinking.Load(msg.ChatID); ok {
		if cf, ok := stop.(*thinkingCancel); ok && cf != nil {
			cf.Cancel()
		}
		c.stopThinking.Delete(msg.ChatID)
	}

	// Delete placeholder before sending voice or text
	if pID, ok := c.placeholders.Load(msg.ChatID); ok {
		c.placeholders.Delete(msg.ChatID)
		_ = c.bot.DeleteMessage(ctx, &telego.DeleteMessageParams{
			ChatID:    tu.ID(chatID),
			MessageID: pID.(int),
		})
	}

	// Send media as photos or documents based on file type
	if len(msg.Media) > 0 {
		for _, mediaURL := range msg.Media {
			if isImageURL(mediaURL) {
				// Send as photo
				photoParams := &telego.SendPhotoParams{
					ChatID: tu.ID(chatID),
					Photo:  tu.FileFromURL(mediaURL),
				}
				if msg.Content != "" {
					photoParams.Caption = markdownToTelegramHTML(msg.Content)
					photoParams.ParseMode = telego.ModeHTML
				}
				if _, photoErr := c.bot.SendPhoto(ctx, photoParams); photoErr != nil {
					logger.ErrorCF("telegram", "Failed to send photo, falling back to text", map[string]interface{}{
						"error": photoErr.Error(),
						"url":   mediaURL,
					})
					break // fall through to text send below
				} else {
					return nil
				}
			} else {
				// Send as document (PDFs, CSVs, generic files)
				docParams := &telego.SendDocumentParams{
					ChatID:   tu.ID(chatID),
					Document: tu.FileFromURL(mediaURL),
				}
				if msg.Content != "" {
					docParams.Caption = markdownToTelegramHTML(msg.Content)
					docParams.ParseMode = telego.ModeHTML
				}
				if _, docErr := c.bot.SendDocument(ctx, docParams); docErr != nil {
					logger.ErrorCF("telegram", "Failed to send document, falling back to text", map[string]interface{}{
						"error": docErr.Error(),
						"url":   mediaURL,
					})
					break // fall through to text send below
				} else {
					return nil
				}
			}
		}
	}

	// Only reply with voice if the user sent a voice/audio message
	if _, wasVoice := c.voiceInput.LoadAndDelete(msg.ChatID); wasVoice {
		plainText := stripMarkdown(msg.Content)
		if len(plainText) > 0 && len(plainText) <= ttsMaxChars && !containsCode(msg.Content) {
			voiceErr := c.sendVoice(ctx, chatID, plainText)
			if voiceErr == nil {
				return nil
			}
			logger.ErrorCF("telegram", "TTS failed, falling back to text", map[string]interface{}{
				"error": voiceErr.Error(),
			})
		}
	}

	// Send as text, splitting if necessary (Telegram limit: 4096 chars)
	htmlContent := markdownToTelegramHTML(msg.Content)
	chunks := splitMessage(htmlContent, 4096)

	for _, chunk := range chunks {
		tgMsg := tu.Message(tu.ID(chatID), chunk)
		tgMsg.ParseMode = telego.ModeHTML

		if _, err = c.bot.SendMessage(ctx, tgMsg); err != nil {
			logger.ErrorCF("telegram", "HTML parse failed, falling back to plain text", map[string]interface{}{
				"error": err.Error(),
			})
			tgMsg.ParseMode = ""
			if _, err = c.bot.SendMessage(ctx, tgMsg); err != nil {
				return err
			}
		}
	}

	return nil
}

// sendVoice converts text to speech and sends as a Telegram voice message.
func (c *TelegramChannel) sendVoice(ctx context.Context, chatID int64, text string) error {
	tmpMP3 := filepath.Join(os.TempDir(), fmt.Sprintf("chango_tts_%d.mp3", time.Now().UnixNano()))
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("chango_tts_%d.ogg", time.Now().UnixNano()))
	defer os.Remove(tmpMP3)
	defer os.Remove(tmpFile)

	// Use edge-tts CLI directly (installed via pip in container)
	ttsCmd := exec.CommandContext(ctx, "edge-tts", "--voice", "es-AR-TomasNeural", "--text", text, "--write-media", tmpMP3)
	if output, err := ttsCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("edge-tts failed: %w: %s", err, string(output))
	}

	// Convert MP3 to OGG for Telegram voice
	ffCmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-i", tmpMP3, "-c:a", "libopus", "-b:a", "64k", tmpFile)
	if output, err := ffCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg failed: %w: %s", err, string(output))
	}

	voiceFile, err := os.Open(tmpFile)
	if err != nil {
		return fmt.Errorf("opening voice file: %w", err)
	}
	defer voiceFile.Close()

	voiceParams := &telego.SendVoiceParams{
		ChatID: tu.ID(chatID),
		Voice:  telego.InputFile{File: voiceFile},
	}
	_, err = c.bot.SendVoice(ctx, voiceParams)
	return err
}

// stripMarkdown removes markdown formatting to get plain text length.
func stripMarkdown(text string) string {
	text = reCodeBlock.ReplaceAllString(text, "")
	text = reInlineCode.ReplaceAllString(text, "")
	text = reBold.ReplaceAllString(text, "$1")
	text = reUnderBold.ReplaceAllString(text, "$1")
	text = reItalic.ReplaceAllString(text, "$1")
	text = reStrike.ReplaceAllString(text, "$1")
	text = reLink.ReplaceAllString(text, "$1")
	text = reHeading.ReplaceAllString(text, "")
	text = reListItem.ReplaceAllString(text, "")
	text = strings.TrimSpace(text)
	return text
}

// containsCode checks if the message has code blocks or inline code.
func containsCode(text string) bool {
	return strings.Contains(text, "```") || reInlineCode.MatchString(text)
}

func (c *TelegramChannel) handleMessage(ctx context.Context, update telego.Update) {
	message := update.Message
	if message == nil {
		return
	}

	user := message.From
	if user == nil {
		return
	}

	userID := fmt.Sprintf("%d", user.ID)
	senderID := userID
	if user.Username != "" {
		senderID = fmt.Sprintf("%s|%s", userID, user.Username)
	}

	// 检查白名单，避免为被拒绝的用户下载附件
	if !c.IsAllowed(userID) && !c.IsAllowed(senderID) {
		logger.DebugCF("telegram", "Message rejected by allowlist", map[string]interface{}{
			"user_id":  userID,
			"username": user.Username,
		})
		return
	}

	chatID := message.Chat.ID
	c.chatIDs[senderID] = chatID

	// Intercept bare /model command → show inline keyboard
	if text := strings.TrimSpace(message.Text); text == "/model" {
		c.sendModelMenu(ctx, chatID)
		return
	}

	content := ""
	mediaPaths := []string{}
	localFiles := []string{} // 跟踪需要清理的本地文件

	// 确保临时文件在函数返回时被清理
	defer func() {
		for _, file := range localFiles {
			if err := os.Remove(file); err != nil {
				logger.DebugCF("telegram", "Failed to cleanup temp file", map[string]interface{}{
					"file":  file,
					"error": err.Error(),
				})
			}
		}
	}()

	if message.Text != "" {
		content += message.Text
	}

	if message.Caption != "" {
		if content != "" {
			content += "\n"
		}
		content += message.Caption
	}

	if message.Photo != nil && len(message.Photo) > 0 {
		photo := message.Photo[len(message.Photo)-1]
		photoPath := c.downloadPhoto(ctx, photo.FileID)
		if photoPath != "" {
			localFiles = append(localFiles, photoPath)
			// Read and base64-encode the image so it survives temp file cleanup
			if imgData, err := os.ReadFile(photoPath); err == nil {
				dataURI := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(imgData)
				mediaPaths = append(mediaPaths, dataURI)
			}
			if content != "" {
				content += "\n"
			}
			content += "[image: photo]"
		}
	}

	// Track whether this input is voice/audio for TTS response
	isVoiceInput := message.Voice != nil || message.Audio != nil
	voiceKey := fmt.Sprintf("%d", chatID)
	if isVoiceInput {
		c.voiceInput.Store(voiceKey, true)
	} else {
		c.voiceInput.Delete(voiceKey)
	}

	if message.Voice != nil {
		voicePath := c.downloadFile(ctx, message.Voice.FileID, ".ogg")
		if voicePath != "" {
			localFiles = append(localFiles, voicePath)
			mediaPaths = append(mediaPaths, voicePath)

			transcribedText := ""
			if c.transcriber != nil && c.transcriber.IsAvailable() {
				ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()

				result, err := c.transcriber.Transcribe(ctx, voicePath)
				if err != nil {
					logger.ErrorCF("telegram", "Voice transcription failed", map[string]interface{}{
						"error": err.Error(),
						"path":  voicePath,
					})
					transcribedText = fmt.Sprintf("[voice (transcription failed)]")
				} else {
					transcribedText = fmt.Sprintf("[voice transcription: %s]", result.Text)
					logger.InfoCF("telegram", "Voice transcribed successfully", map[string]interface{}{
						"text": result.Text,
					})
				}
			} else {
				transcribedText = fmt.Sprintf("[voice]")
			}

			if content != "" {
				content += "\n"
			}
			content += transcribedText
		}
	}

	if message.Audio != nil {
		audioPath := c.downloadFile(ctx, message.Audio.FileID, ".mp3")
		if audioPath != "" {
			localFiles = append(localFiles, audioPath)
			mediaPaths = append(mediaPaths, audioPath)
			if content != "" {
				content += "\n"
			}
			content += fmt.Sprintf("[audio]")
		}
	}

	if message.Document != nil {
		docPath := c.downloadFile(ctx, message.Document.FileID, "")
		if docPath != "" {
			localFiles = append(localFiles, docPath)
			mimeType := ""
			if message.Document.MimeType != "" {
				mimeType = message.Document.MimeType
			}
			fileName := ""
			if message.Document.FileName != "" {
				fileName = message.Document.FileName
			}

			// Extract text from PDFs using pdftotext
			if mimeType == "application/pdf" || strings.HasSuffix(strings.ToLower(fileName), ".pdf") {
				pdfText := c.extractPDFText(docPath)
				if pdfText != "" {
					if content != "" {
						content += "\n"
					}
					content += fmt.Sprintf("[PDF: %s]\n%s", fileName, pdfText)
				} else {
					if content != "" {
						content += "\n"
					}
					content += fmt.Sprintf("[PDF: %s - no se pudo extraer texto]", fileName)
				}
			} else {
				mediaPaths = append(mediaPaths, docPath)
				if content != "" {
					content += "\n"
				}
				content += fmt.Sprintf("[file: %s]", fileName)
			}
		}
	}

	if content == "" {
		content = "[empty message]"
	}

	logger.DebugCF("telegram", "Received message", map[string]interface{}{
		"sender_id": senderID,
		"chat_id":   fmt.Sprintf("%d", chatID),
		"preview":   utils.Truncate(content, 50),
	})

	// Thinking indicator
	err := c.bot.SendChatAction(ctx, tu.ChatAction(tu.ID(chatID), telego.ChatActionTyping))
	if err != nil {
		logger.ErrorCF("telegram", "Failed to send chat action", map[string]interface{}{
			"error": err.Error(),
		})
	}

	// Stop any previous thinking animation
	chatIDStr := fmt.Sprintf("%d", chatID)
	if prevStop, ok := c.stopThinking.Load(chatIDStr); ok {
		if cf, ok := prevStop.(*thinkingCancel); ok && cf != nil {
			cf.Cancel()
		}
	}

	// Create cancel function for thinking state
	_, thinkCancel := context.WithTimeout(ctx, 5*time.Minute)
	c.stopThinking.Store(chatIDStr, &thinkingCancel{fn: thinkCancel})

	pMsg, err := c.bot.SendMessage(ctx, tu.Message(tu.ID(chatID), "Thinking... 💭"))
	if err == nil {
		pID := pMsg.MessageID
		c.placeholders.Store(chatIDStr, pID)
	}

	metadata := map[string]string{
		"message_id": fmt.Sprintf("%d", message.MessageID),
		"user_id":    fmt.Sprintf("%d", user.ID),
		"username":   user.Username,
		"first_name": user.FirstName,
		"is_group":   fmt.Sprintf("%t", message.Chat.Type != "private"),
	}

	c.HandleMessage(senderID, fmt.Sprintf("%d", chatID), content, mediaPaths, metadata)
}

func (c *TelegramChannel) downloadPhoto(ctx context.Context, fileID string) string {
	file, err := c.bot.GetFile(ctx, &telego.GetFileParams{FileID: fileID})
	if err != nil {
		logger.ErrorCF("telegram", "Failed to get photo file", map[string]interface{}{
			"error": err.Error(),
		})
		return ""
	}

	return c.downloadFileWithInfo(file, ".jpg")
}

func (c *TelegramChannel) downloadFileWithInfo(file *telego.File, ext string) string {
	if file.FilePath == "" {
		return ""
	}

	url := c.bot.FileDownloadURL(file.FilePath)
	logger.DebugCF("telegram", "File URL", map[string]interface{}{"url": url})

	// Use FilePath as filename for better identification
	filename := file.FilePath + ext
	return utils.DownloadFile(url, filename, utils.DownloadOptions{
		LoggerPrefix: "telegram",
	})
}

func (c *TelegramChannel) downloadFile(ctx context.Context, fileID, ext string) string {
	file, err := c.bot.GetFile(ctx, &telego.GetFileParams{FileID: fileID})
	if err != nil {
		logger.ErrorCF("telegram", "Failed to get file", map[string]interface{}{
			"error": err.Error(),
		})
		return ""
	}

	return c.downloadFileWithInfo(file, ext)
}

// extractPDFText uses pdftotext to extract text from a PDF file.
// Returns extracted text (truncated to 15000 chars to avoid context overflow).
func (c *TelegramChannel) extractPDFText(pdfPath string) string {
	cmd := exec.Command("pdftotext", "-layout", pdfPath, "-")
	output, err := cmd.Output()
	if err != nil {
		logger.ErrorCF("telegram", "Failed to extract PDF text", map[string]interface{}{
			"path":  pdfPath,
			"error": err.Error(),
		})
		return ""
	}

	text := strings.TrimSpace(string(output))
	if len(text) > 15000 {
		text = text[:15000] + "\n\n[... texto truncado, PDF muy largo ...]"
	}

	logger.InfoCF("telegram", "PDF text extracted", map[string]interface{}{
		"path":  pdfPath,
		"chars": len(text),
	})
	return text
}

func (c *TelegramChannel) sendModelMenu(ctx context.Context, chatID int64) {
	models := c.appConfig.Agents.Defaults.AvailableModels
	if len(models) == 0 {
		models = defaultModels
	}
	currentModel := c.appConfig.Agents.Defaults.Model

	var buttons []telego.InlineKeyboardButton
	for _, m := range models {
		label := m
		if m == currentModel {
			label = "\u2705 " + m
		}
		buttons = append(buttons, tu.InlineKeyboardButton(label).WithCallbackData("model:"+m))
	}

	keyboard := tu.InlineKeyboardGrid(tu.InlineKeyboardCols(2, buttons...))
	msg := tu.Message(tu.ID(chatID), "Elegí un modelo:")
	msg.ReplyMarkup = keyboard

	if _, err := c.bot.SendMessage(ctx, msg); err != nil {
		logger.ErrorCF("telegram", "Failed to send model menu", map[string]interface{}{
			"error": err.Error(),
		})
	}
}

func (c *TelegramChannel) handleCallbackQuery(ctx context.Context, update telego.Update) {
	query := update.CallbackQuery
	if query == nil || !strings.HasPrefix(query.Data, "model:") {
		return
	}

	modelName := strings.TrimPrefix(query.Data, "model:")

	// Answer the callback to dismiss the spinner
	_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Cambiando a "+modelName+"..."))

	// Edit original message to show selection, remove buttons
	if query.Message != nil {
		editParams := &telego.EditMessageTextParams{
			ChatID:    tu.ID(query.Message.GetChat().ID),
			MessageID: query.Message.GetMessageID(),
			Text:      "\u2705 Modelo: " + modelName,
		}
		_, _ = c.bot.EditMessageText(ctx, editParams)
	}

	// Publish to bus so AgentLoop handles the actual model change
	userID := fmt.Sprintf("%d", query.From.ID)
	senderID := userID
	if query.From.Username != "" {
		senderID = fmt.Sprintf("%s|%s", userID, query.From.Username)
	}
	chatIDStr := ""
	if query.Message != nil {
		chatIDStr = fmt.Sprintf("%d", query.Message.GetChat().ID)
	}

	c.HandleMessage(senderID, chatIDStr, "/model "+modelName, nil, map[string]string{
		"user_id":  userID,
		"username": query.From.Username,
	})
}

// splitMessage splits a message into chunks that fit within Telegram's character limit.
// It tries to split at paragraph boundaries (\n\n), then at line breaks (\n),
// and as a last resort at the exact limit.
func splitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	for len(text) > 0 {
		if len(text) <= maxLen {
			chunks = append(chunks, text)
			break
		}

		// Try to split at paragraph boundary
		chunk := text[:maxLen]
		splitAt := strings.LastIndex(chunk, "\n\n")
		if splitAt < maxLen/2 {
			// Try line break
			splitAt = strings.LastIndex(chunk, "\n")
		}
		if splitAt < maxLen/4 {
			// Hard split
			splitAt = maxLen
		}

		chunks = append(chunks, strings.TrimSpace(text[:splitAt]))
		text = strings.TrimSpace(text[splitAt:])
	}

	return chunks
}

// isImageURL returns true if the URL points to a known image format or is a data URI image.
func isImageURL(u string) bool {
	if strings.HasPrefix(u, "data:image/") {
		return true
	}
	lower := strings.ToLower(u)
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".gif", ".webp"} {
		if strings.HasSuffix(lower, ext) || strings.Contains(lower, ext+"?") {
			return true
		}
	}
	return false
}

func parseChatID(chatIDStr string) (int64, error) {
	var id int64
	_, err := fmt.Sscanf(chatIDStr, "%d", &id)
	return id, err
}

func markdownToTelegramHTML(text string) string {
	if text == "" {
		return ""
	}

	codeBlocks := extractCodeBlocks(text)
	text = codeBlocks.text

	inlineCodes := extractInlineCodes(text)
	text = inlineCodes.text

	text = reHeading.ReplaceAllString(text, "$1")

	text = reBlockquote.ReplaceAllString(text, "$1")

	text = escapeHTML(text)

	text = reLink.ReplaceAllString(text, `<a href="$2">$1</a>`)

	text = reBold.ReplaceAllString(text, "<b>$1</b>")

	text = reUnderBold.ReplaceAllString(text, "<b>$1</b>")

	text = reItalic.ReplaceAllStringFunc(text, func(s string) string {
		match := reItalic.FindStringSubmatch(s)
		if len(match) < 2 {
			return s
		}
		return "<i>" + match[1] + "</i>"
	})

	text = reStrike.ReplaceAllString(text, "<s>$1</s>")

	text = reListItem.ReplaceAllString(text, "• ")

	for i, code := range inlineCodes.codes {
		escaped := escapeHTML(code)
		text = strings.ReplaceAll(text, fmt.Sprintf("\x00IC%d\x00", i), fmt.Sprintf("<code>%s</code>", escaped))
	}

	for i, code := range codeBlocks.codes {
		escaped := escapeHTML(code)
		text = strings.ReplaceAll(text, fmt.Sprintf("\x00CB%d\x00", i), fmt.Sprintf("<pre><code>%s</code></pre>", escaped))
	}

	return text
}

type codeBlockMatch struct {
	text  string
	codes []string
}

func extractCodeBlocks(text string) codeBlockMatch {
	matches := reCodeBlockCap.FindAllStringSubmatch(text, -1)

	codes := make([]string, 0, len(matches))
	for _, match := range matches {
		codes = append(codes, match[1])
	}

	i := 0
	text = reCodeBlockCap.ReplaceAllStringFunc(text, func(m string) string {
		placeholder := fmt.Sprintf("\x00CB%d\x00", i)
		i++
		return placeholder
	})

	return codeBlockMatch{text: text, codes: codes}
}

type inlineCodeMatch struct {
	text  string
	codes []string
}

func extractInlineCodes(text string) inlineCodeMatch {
	reCapture := regexp.MustCompile("`([^`]+)`")
	matches := reCapture.FindAllStringSubmatch(text, -1)

	codes := make([]string, 0, len(matches))
	for _, match := range matches {
		codes = append(codes, match[1])
	}

	i := 0
	text = reInlineCode.ReplaceAllStringFunc(text, func(m string) string {
		placeholder := fmt.Sprintf("\x00IC%d\x00", i)
		i++
		return placeholder
	})

	return inlineCodeMatch{text: text, codes: codes}
}

func escapeHTML(text string) string {
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	return text
}
