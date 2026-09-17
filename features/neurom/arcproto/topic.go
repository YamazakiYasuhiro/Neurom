package arcproto

// Bus topics. Commands travel on a module's own topic; the module reports back
// on the matching *_update topic.
const (
	TopicVRAM          = "vram"
	TopicVRAMUpdate    = "vram_update"
	TopicMonitor       = "monitor"
	TopicMonitorUpdate = "monitor_update"
	TopicSystem        = "system"
	TopicIO            = "io"
)
