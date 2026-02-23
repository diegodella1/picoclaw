package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Magic Home protocol constants
const (
	mhDiscoveryPort = 48899
	mhControlPort   = 5577
	mhDiscoveryMsg  = "HF-A11ASSISTHREAD"
	mhDiscoveryWait = 3 * time.Second
	mhTCPTimeout    = 5 * time.Second
)

// Magic Home command bytes
var (
	mhCmdOn    = []byte{0x71, 0x23, 0x0F}
	mhCmdOff   = []byte{0x71, 0x24, 0x0F}
	mhCmdQuery = []byte{0x81, 0x8A, 0x8B}
)

// Predefined patterns with codes
var mhPatterns = map[string]byte{
	"7color_cross_fade":    0x25,
	"red_gradual":          0x26,
	"green_gradual":        0x27,
	"blue_gradual":         0x28,
	"yellow_gradual":       0x29,
	"cyan_gradual":         0x2A,
	"purple_gradual":       0x2B,
	"white_gradual":        0x2C,
	"red_green_cross_fade": 0x2D,
	"red_blue_cross_fade":  0x2E,
	"green_blue_cross_fade": 0x2F,
	"7color_strobe":        0x30,
	"red_strobe":           0x31,
	"green_strobe":         0x32,
	"blue_strobe":          0x33,
	"yellow_strobe":        0x34,
	"cyan_strobe":          0x35,
	"purple_strobe":        0x36,
	"white_strobe":         0x37,
	"7color_jump":          0x38,
}

// LightDevice represents a saved Magic Home device
type LightDevice struct {
	IP        string `json:"ip"`
	MAC       string `json:"mac"`
	Model     string `json:"model"`
	Name      string `json:"name"`
	Type      string `json:"type"` // led_strip | lightbulb | rgb_controller | unknown
	SavedAt   string `json:"saved_at"`
	UpdatedAt string `json:"updated_at"`
}

// discoveredDevice is a raw discovery result
type discoveredDevice struct {
	IP    string
	MAC   string
	Model string
}

// LightsTool controls Magic Home WiFi LED devices
type LightsTool struct {
	filePath string
	mu       sync.Mutex
}

func NewLightsTool(workspace string) *LightsTool {
	return &LightsTool{
		filePath: filepath.Join(workspace, "lights.json"),
	}
}

func (t *LightsTool) Name() string { return "lights" }

func (t *LightsTool) Description() string {
	return "Control Magic Home WiFi smart lights (LED strips, bulbs). Actions: discover (scan network), save (name a device), remove, list, on, off, color (RGB), brightness, pattern, status."
}

func (t *LightsTool) Parameters() map[string]interface{} {
	patternNames := make([]string, 0, len(mhPatterns))
	for name := range mhPatterns {
		patternNames = append(patternNames, name)
	}

	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"discover", "save", "remove", "list", "on", "off", "color", "brightness", "pattern", "status"},
				"description": "Action to perform",
			},
			"device": map[string]interface{}{
				"type":        "string",
				"description": "Device name or IP (required for on/off/color/brightness/pattern/status/remove)",
			},
			"name": map[string]interface{}{
				"type":        "string",
				"description": "Friendly name to assign (required for save)",
			},
			"ip": map[string]interface{}{
				"type":        "string",
				"description": "Device IP address (required for save)",
			},
			"device_type": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"led_strip", "lightbulb", "rgb_controller", "unknown"},
				"description": "Device type (optional for save)",
			},
			"mac": map[string]interface{}{
				"type":        "string",
				"description": "MAC address (optional for save, from discover results)",
			},
			"model": map[string]interface{}{
				"type":        "string",
				"description": "Model string (optional for save, from discover results)",
			},
			"r": map[string]interface{}{
				"type":        "number",
				"description": "Red channel 0-255 (for color action)",
			},
			"g": map[string]interface{}{
				"type":        "number",
				"description": "Green channel 0-255 (for color action)",
			},
			"b": map[string]interface{}{
				"type":        "number",
				"description": "Blue channel 0-255 (for color action)",
			},
			"brightness": map[string]interface{}{
				"type":        "number",
				"description": "Brightness level 1-100 (for brightness action)",
			},
			"pattern": map[string]interface{}{
				"type":        "string",
				"enum":        patternNames,
				"description": "Pattern name (for pattern action)",
			},
			"speed": map[string]interface{}{
				"type":        "number",
				"description": "Pattern speed 1-100, default 50 (for pattern action)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *LightsTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	action, _ := args["action"].(string)
	switch action {
	case "discover":
		return t.discover()
	case "save":
		return t.save(args)
	case "remove":
		return t.remove(args)
	case "list":
		return t.list()
	case "on":
		return t.power(args, true)
	case "off":
		return t.power(args, false)
	case "color":
		return t.color(args)
	case "brightness":
		return t.setBrightness(args)
	case "pattern":
		return t.setPattern(args)
	case "status":
		return t.status(args)
	default:
		return ErrorResult(fmt.Sprintf("unknown action: %s", action))
	}
}

// --- Discovery ---

func (t *LightsTool) discover() *ToolResult {
	devices, err := mhDiscover()
	if err != nil {
		return ErrorResult(fmt.Sprintf("discovery failed: %v", err))
	}
	if len(devices) == 0 {
		return SilentResult("No Magic Home devices found on the network. Make sure devices are powered on and connected to the same WiFi network.")
	}

	// Load saved devices to mark which ones are already saved
	saved := t.loadDevices()
	savedByIP := make(map[string]string)
	for _, d := range saved {
		savedByIP[d.IP] = d.Name
	}

	var lines []string
	for i, d := range devices {
		devType := classifyFromModel(d.Model)
		line := fmt.Sprintf("%d. IP: %s | MAC: %s | Model: %s | Type: %s", i+1, d.IP, d.MAC, d.Model, devType)
		if name, ok := savedByIP[d.IP]; ok {
			line += fmt.Sprintf(" | Saved as: %s", name)
		}
		lines = append(lines, line)
	}

	return SilentResult(fmt.Sprintf("Found %d device(s):\n%s", len(devices), strings.Join(lines, "\n")))
}

// --- Persistence ---

func (t *LightsTool) save(args map[string]interface{}) *ToolResult {
	name, _ := args["name"].(string)
	ip, _ := args["ip"].(string)
	if name == "" || ip == "" {
		return ErrorResult("name and ip are required for save")
	}

	devType, _ := args["device_type"].(string)
	mac, _ := args["mac"].(string)
	model, _ := args["model"].(string)

	t.mu.Lock()
	defer t.mu.Unlock()

	devices := t.loadDevices()

	// Check for duplicate name
	for _, d := range devices {
		if strings.EqualFold(d.Name, name) && d.IP != ip {
			return ErrorResult(fmt.Sprintf("a device named '%s' already exists (IP: %s). Remove it first or use a different name.", name, d.IP))
		}
	}

	if devType == "" && model != "" {
		devType = classifyFromModel(model)
	}
	if devType == "" {
		devType = "unknown"
	}

	now := time.Now().Format(time.RFC3339)

	// Update existing or append
	found := false
	for i, d := range devices {
		if d.IP == ip {
			devices[i].Name = name
			devices[i].Type = devType
			devices[i].UpdatedAt = now
			if mac != "" {
				devices[i].MAC = mac
			}
			if model != "" {
				devices[i].Model = model
			}
			found = true
			break
		}
	}

	if !found {
		devices = append(devices, LightDevice{
			IP:        ip,
			MAC:       mac,
			Model:     model,
			Name:      name,
			Type:      devType,
			SavedAt:   now,
			UpdatedAt: now,
		})
	}

	if err := t.saveDevices(devices); err != nil {
		return ErrorResult(fmt.Sprintf("failed to save: %v", err))
	}

	return SilentResult(fmt.Sprintf("Device '%s' saved (IP: %s, type: %s)", name, ip, devType))
}

func (t *LightsTool) remove(args map[string]interface{}) *ToolResult {
	identifier, _ := args["device"].(string)
	if identifier == "" {
		return ErrorResult("device is required for remove")
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	devices := t.loadDevices()
	idx := resolveDeviceIndex(devices, identifier)
	if idx < 0 {
		return ErrorResult(fmt.Sprintf("device '%s' not found", identifier))
	}

	removed := devices[idx]
	devices = append(devices[:idx], devices[idx+1:]...)

	if err := t.saveDevices(devices); err != nil {
		return ErrorResult(fmt.Sprintf("failed to save: %v", err))
	}

	return SilentResult(fmt.Sprintf("Device '%s' (IP: %s) removed", removed.Name, removed.IP))
}

func (t *LightsTool) list() *ToolResult {
	t.mu.Lock()
	devices := t.loadDevices()
	t.mu.Unlock()

	if len(devices) == 0 {
		return SilentResult("No saved devices. Use 'discover' to find devices, then 'save' to name them.")
	}

	var lines []string
	for _, d := range devices {
		lines = append(lines, fmt.Sprintf("- %s | IP: %s | Type: %s | MAC: %s", d.Name, d.IP, d.Type, d.MAC))
	}

	return SilentResult(fmt.Sprintf("%d saved device(s):\n%s", len(devices), strings.Join(lines, "\n")))
}

// --- Control ---

func (t *LightsTool) power(args map[string]interface{}, on bool) *ToolResult {
	dev, err := t.resolveDevice(args)
	if err != nil {
		return ErrorResult(err.Error())
	}

	var cmd []byte
	if on {
		cmd = mhCmdOn
	} else {
		cmd = mhCmdOff
	}

	if err := mhSendCommand(dev.IP, cmd); err != nil {
		return ErrorResult(fmt.Sprintf("failed to send command to %s (%s): %v", dev.Name, dev.IP, err))
	}

	state := "ON"
	if !on {
		state = "OFF"
	}
	return SilentResult(fmt.Sprintf("'%s' turned %s", dev.Name, state))
}

func (t *LightsTool) color(args map[string]interface{}) *ToolResult {
	dev, err := t.resolveDevice(args)
	if err != nil {
		return ErrorResult(err.Error())
	}

	r := clampByte(args, "r")
	g := clampByte(args, "g")
	b := clampByte(args, "b")

	cmd := []byte{0x31, r, g, b, 0x00, 0xF0, 0x0F}
	if err := mhSendCommand(dev.IP, cmd); err != nil {
		return ErrorResult(fmt.Sprintf("failed to set color on %s: %v", dev.Name, err))
	}

	return SilentResult(fmt.Sprintf("'%s' color set to RGB(%d, %d, %d)", dev.Name, r, g, b))
}

func (t *LightsTool) setBrightness(args map[string]interface{}) *ToolResult {
	dev, err := t.resolveDevice(args)
	if err != nil {
		return ErrorResult(err.Error())
	}

	brightness := getFloat(args, "brightness")
	if brightness < 1 {
		brightness = 1
	}
	if brightness > 100 {
		brightness = 100
	}

	// Query current color first
	state, err := mhQueryState(dev.IP)
	if err != nil {
		// Fallback: set white at the given brightness
		level := byte(brightness * 255 / 100)
		cmd := []byte{0x31, level, level, level, 0x00, 0xF0, 0x0F}
		if err2 := mhSendCommand(dev.IP, cmd); err2 != nil {
			return ErrorResult(fmt.Sprintf("failed to set brightness on %s: %v", dev.Name, err2))
		}
		return SilentResult(fmt.Sprintf("'%s' brightness set to %d%% (white fallback, couldn't query current color)", dev.Name, int(brightness)))
	}

	// Scale current RGB proportionally
	r, g, b := state.r, state.g, state.b
	maxC := maxByte(r, g, b)
	if maxC == 0 {
		maxC = 1 // avoid div by zero, will set to white
		r, g, b = 255, 255, 255
	}

	factor := (brightness / 100.0) / (float64(maxC) / 255.0)
	nr := byte(math.Min(255, math.Round(float64(r)*factor)))
	ng := byte(math.Min(255, math.Round(float64(g)*factor)))
	nb := byte(math.Min(255, math.Round(float64(b)*factor)))

	cmd := []byte{0x31, nr, ng, nb, 0x00, 0xF0, 0x0F}
	if err := mhSendCommand(dev.IP, cmd); err != nil {
		return ErrorResult(fmt.Sprintf("failed to set brightness on %s: %v", dev.Name, err))
	}

	return SilentResult(fmt.Sprintf("'%s' brightness set to %d%%", dev.Name, int(brightness)))
}

func (t *LightsTool) setPattern(args map[string]interface{}) *ToolResult {
	dev, err := t.resolveDevice(args)
	if err != nil {
		return ErrorResult(err.Error())
	}

	patternName, _ := args["pattern"].(string)
	if patternName == "" {
		// List available patterns
		var names []string
		for name := range mhPatterns {
			names = append(names, name)
		}
		return SilentResult(fmt.Sprintf("Available patterns: %s", strings.Join(names, ", ")))
	}

	code, ok := mhPatterns[patternName]
	if !ok {
		// Try fuzzy match
		patternLower := strings.ToLower(patternName)
		for name, c := range mhPatterns {
			if strings.Contains(strings.ToLower(name), patternLower) {
				code = c
				patternName = name
				ok = true
				break
			}
		}
		if !ok {
			var names []string
			for name := range mhPatterns {
				names = append(names, name)
			}
			return ErrorResult(fmt.Sprintf("unknown pattern '%s'. Available: %s", patternName, strings.Join(names, ", ")))
		}
	}

	speed := getFloat(args, "speed")
	if speed <= 0 {
		speed = 50
	}
	if speed > 100 {
		speed = 100
	}
	// Speed is inverted: 1 = slowest (0x1F = 31), 100 = fastest (0x01 = 1)
	delay := byte(31 - int(speed*30/100))
	if delay < 1 {
		delay = 1
	}

	cmd := []byte{0x61, code, delay, 0x0F}
	if err := mhSendCommand(dev.IP, cmd); err != nil {
		return ErrorResult(fmt.Sprintf("failed to set pattern on %s: %v", dev.Name, err))
	}

	return SilentResult(fmt.Sprintf("'%s' pattern set to '%s' (speed: %d%%)", dev.Name, patternName, int(speed)))
}

func (t *LightsTool) status(args map[string]interface{}) *ToolResult {
	dev, err := t.resolveDevice(args)
	if err != nil {
		return ErrorResult(err.Error())
	}

	state, err := mhQueryState(dev.IP)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to query %s (%s): %v", dev.Name, dev.IP, err))
	}

	powerStr := "OFF"
	if state.on {
		powerStr = "ON"
	}

	modeStr := "static color"
	if state.mode != 0x61 && state.mode != 0x41 {
		modeStr = fmt.Sprintf("pattern (0x%02X)", state.mode)
	}

	brightnessPercent := int(float64(maxByte(state.r, state.g, state.b)) / 255.0 * 100)

	return SilentResult(fmt.Sprintf("'%s' status:\n- Power: %s\n- Color: RGB(%d, %d, %d)\n- Brightness: ~%d%%\n- Mode: %s",
		dev.Name, powerStr, state.r, state.g, state.b, brightnessPercent, modeStr))
}

// --- Device resolution ---

func (t *LightsTool) resolveDevice(args map[string]interface{}) (*LightDevice, error) {
	identifier, _ := args["device"].(string)
	if identifier == "" {
		return nil, fmt.Errorf("device is required")
	}

	t.mu.Lock()
	devices := t.loadDevices()
	t.mu.Unlock()

	idx := resolveDeviceIndex(devices, identifier)
	if idx < 0 {
		return nil, fmt.Errorf("device '%s' not found. Use 'list' to see saved devices", identifier)
	}
	return &devices[idx], nil
}

func resolveDeviceIndex(devices []LightDevice, identifier string) int {
	id := strings.ToLower(identifier)

	// Exact name match (case-insensitive)
	for i, d := range devices {
		if strings.ToLower(d.Name) == id {
			return i
		}
	}

	// Partial name match (contains)
	for i, d := range devices {
		if strings.Contains(strings.ToLower(d.Name), id) {
			return i
		}
	}

	// IP match
	for i, d := range devices {
		if d.IP == identifier {
			return i
		}
	}

	return -1
}

// --- Persistence helpers ---

func (t *LightsTool) loadDevices() []LightDevice {
	data, err := os.ReadFile(t.filePath)
	if err != nil {
		return nil
	}
	var devices []LightDevice
	if err := json.Unmarshal(data, &devices); err != nil {
		return nil
	}
	return devices
}

func (t *LightsTool) saveDevices(devices []LightDevice) error {
	data, err := json.MarshalIndent(devices, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := t.filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, t.filePath)
}

// --- Magic Home protocol ---

func mhDiscover() ([]discoveredDevice, error) {
	// If running inside a Docker container, UDP broadcast won't reach the LAN.
	// Use nsenter to run discovery from the host's network namespace.
	if _, err := os.Stat("/hostfs/proc/1/ns/net"); err == nil {
		return mhDiscoverViaHost()
	}
	return mhDiscoverDirect()
}

// mhDiscoverViaHost runs discovery via nsenter into the host network namespace.
// This is needed when running in a Docker container with bridge networking.
func mhDiscoverViaHost() ([]discoveredDevice, error) {
	// Python script that does the UDP broadcast discovery from host network
	script := `
import socket, json
sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
sock.setsockopt(socket.SOL_SOCKET, socket.SO_BROADCAST, 1)
sock.settimeout(3)
sock.sendto(b'HF-A11ASSISTHREAD', ('255.255.255.255', 48899))
devices = []
seen = set()
try:
    while True:
        data, addr = sock.recvfrom(1024)
        msg = data.decode().strip()
        if msg == 'HF-A11ASSISTHREAD':
            continue
        parts = msg.split(',', 2)
        if len(parts) >= 3 and parts[0] not in seen:
            seen.add(parts[0])
            devices.append({"ip": parts[0].strip(), "mac": parts[1].strip(), "model": parts[2].strip()})
except socket.timeout:
    pass
sock.close()
print(json.dumps(devices))
`
	cmd := exec.Command("nsenter",
		"--net=/hostfs/proc/1/ns/net",
		"--", "python3", "-c", script)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("nsenter discovery failed: %v (stderr: %s)", err, stderr.String())
	}

	var devices []discoveredDevice
	if err := json.Unmarshal(stdout.Bytes(), &devices); err != nil {
		return nil, fmt.Errorf("failed to parse discovery output: %v", err)
	}
	return devices, nil
}

// mhDiscoverDirect does UDP broadcast discovery directly (works on host or --network host).
func mhDiscoverDirect() ([]discoveredDevice, error) {
	addr, err := net.ResolveUDPAddr("udp4", "255.255.255.255:48899")
	if err != nil {
		return nil, err
	}

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: 0})
	if err != nil {
		return nil, fmt.Errorf("failed to open UDP socket: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(mhDiscoveryWait))

	_, err = conn.WriteToUDP([]byte(mhDiscoveryMsg), addr)
	if err != nil {
		return nil, fmt.Errorf("failed to send discovery broadcast: %v", err)
	}

	var devices []discoveredDevice
	seen := make(map[string]bool)
	buf := make([]byte, 1024)

	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			// Timeout is expected
			break
		}

		response := strings.TrimSpace(string(buf[:n]))
		// Skip echo of our own message
		if response == mhDiscoveryMsg {
			continue
		}

		// Response format: "IP,MAC,MODEL"
		parts := strings.SplitN(response, ",", 3)
		if len(parts) < 3 {
			continue
		}

		ip := strings.TrimSpace(parts[0])
		if seen[ip] {
			continue
		}
		seen[ip] = true

		devices = append(devices, discoveredDevice{
			IP:    ip,
			MAC:   strings.TrimSpace(parts[1]),
			Model: strings.TrimSpace(parts[2]),
		})
	}

	return devices, nil
}

func mhSendCommand(ip string, cmd []byte) error {
	// Append checksum
	packet := make([]byte, len(cmd)+1)
	copy(packet, cmd)
	packet[len(cmd)] = mhChecksum(cmd)

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, mhControlPort), mhTCPTimeout)
	if err != nil {
		return fmt.Errorf("connection failed: %v", err)
	}
	defer conn.Close()

	conn.SetWriteDeadline(time.Now().Add(mhTCPTimeout))
	_, err = conn.Write(packet)
	return err
}

type mhState struct {
	on   bool
	mode byte
	r, g, b byte
}

func mhQueryState(ip string) (*mhState, error) {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, mhControlPort), mhTCPTimeout)
	if err != nil {
		return nil, fmt.Errorf("connection failed: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(mhTCPTimeout))

	// Send query command with checksum
	packet := make([]byte, len(mhCmdQuery)+1)
	copy(packet, mhCmdQuery)
	packet[len(mhCmdQuery)] = mhChecksum(mhCmdQuery)
	_, err = conn.Write(packet)
	if err != nil {
		return nil, fmt.Errorf("write failed: %v", err)
	}

	// Read 14-byte response
	buf := make([]byte, 14)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("read failed: %v", err)
	}
	if n < 14 {
		return nil, fmt.Errorf("short response: got %d bytes, expected 14", n)
	}

	// Parse response:
	// [0]=0x81, [1]=device_type, [2]=power (0x23=ON, 0x24=OFF),
	// [3]=mode, [4]=??, [5]=speed,
	// [6]=R, [7]=G, [8]=B, [9]=W, ...
	return &mhState{
		on:   buf[2] == 0x23,
		mode: buf[3],
		r:    buf[6],
		g:    buf[7],
		b:    buf[8],
	}, nil
}

func mhChecksum(data []byte) byte {
	var sum byte
	for _, b := range data {
		sum += b
	}
	return sum & 0xFF
}

// --- Helpers ---

func classifyFromModel(model string) string {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "strip") || strings.Contains(m, "led") || strings.Contains(m, "tape"):
		return "led_strip"
	case strings.Contains(m, "bulb") || strings.Contains(m, "lamp") || strings.Contains(m, "light"):
		return "lightbulb"
	case strings.Contains(m, "controller") || strings.Contains(m, "rgb"):
		return "rgb_controller"
	default:
		return "unknown"
	}
}

func clampByte(args map[string]interface{}, key string) byte {
	v := getFloat(args, key)
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v)
}

func getFloat(args map[string]interface{}, key string) float64 {
	switch v := args[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	default:
		return 0
	}
}

func maxByte(a, b, c byte) byte {
	m := a
	if b > m {
		m = b
	}
	if c > m {
		m = c
	}
	return m
}
