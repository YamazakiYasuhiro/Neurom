package arcproto

// Command targets accepted by the VRAM module. These are the strings the module
// dispatches on, so they are part of the wire protocol.
const (
	TargetMode              = "mode"
	TargetDrawPixel         = "draw_pixel"
	TargetSetPalette        = "set_palette"
	TargetSetPaletteBlock   = "set_palette_block"
	TargetReadPaletteBlock  = "read_palette_block"
	TargetClearVRAM         = "clear_vram"
	TargetBlitRect          = "blit_rect"
	TargetBlitRectTransform = "blit_rect_transform"
	TargetReadRect          = "read_rect"
	TargetCopyRect          = "copy_rect"
	TargetSetPageCount      = "set_page_count"
	TargetSetDisplayPage    = "set_display_page"
	TargetSwapPages         = "swap_pages"
	TargetCopyPage          = "copy_page"
	TargetSetPageSize       = "set_page_size"
	TargetSetViewport       = "set_viewport"
	TargetGetStats          = "get_stats"
)

// Events published by the VRAM module on TopicVRAMUpdate.
//
// Most events echo the payload of the command that caused them and exist so the
// monitor can invalidate the affected region. Only the events with a decoder in
// this package carry a layout of their own.
const (
	EventModeChanged         = "mode_changed"
	EventVRAMUpdated         = "vram_updated"
	EventVRAMCleared         = "vram_cleared"
	EventRectUpdated         = "rect_updated"
	EventRectCopied          = "rect_copied"
	EventRectData            = "rect_data"
	EventPaletteUpdated      = "palette_updated"
	EventPaletteBlockUpdated = "palette_block_updated"
	EventPaletteData         = "palette_data"
	EventPageCountChanged    = "page_count_changed"
	EventDisplayPageChanged  = "display_page_changed"
	EventPagesSwapped        = "pages_swapped"
	EventPageCopied          = "page_copied"
	EventPageSizeChanged     = "page_size_changed"
	EventViewportChanged     = "viewport_changed"
	EventPageError           = "page_error"
	EventStatsData           = "stats_data"
)
