package cpu

import (
	"context"
	"time"

	"github.com/axsh/neurom/internal/bus"
)

type CPUModule struct{}

func New() *CPUModule {
	return &CPUModule{}
}

func (c *CPUModule) Name() string {
	return "CPU"
}

func (c *CPUModule) Start(ctx context.Context, b bus.Bus) error {
	go c.run(ctx, b)
	return nil
}

func (c *CPUModule) Stop() error {
	return nil
}

func (c *CPUModule) run(ctx context.Context, b bus.Bus) {
	// Initialize display via VRAM
	_ = b.Publish("vram", &bus.BusMessage{
		Target:    "mode",
		Operation: bus.OpCommand,
		Data:      []byte{0x00, 0x01, 0x00},
		Source:    c.Name(),
	})

	// VRAM/Monitorの初期化を少し待つ
	time.Sleep(300 * time.Millisecond)

	// デモ: 画面中央に 128x128 の虹色スクロール用ブロックを描画 (インデックス 1〜16を使用)
	for y := 42; y < 42+128; y++ {
		p := byte(((y - 42) / 8) % 16) + 1 // インデックス 1 から 16 までのボーダー
		hiY, loY := byte((y>>8)&0xFF), byte(y&0xFF)

		for x := 64; x < 64+128; x++ {
			hiX, loX := byte((x>>8)&0xFF), byte(x&0xFF)
			_ = b.Publish("vram", &bus.BusMessage{
				Target:    "draw_pixel",
				Operation: bus.OpCommand,
				Data:      []byte{hiX, loX, hiY, loY, p},
				Source:    c.Name(),
			})
		}
	}

	// パレットアニメーション (カラーサイクリング) を開始
	ticker := time.NewTicker(33 * time.Millisecond) // 約30FPS
	defer ticker.Stop()

	offset := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// インデックス 1〜16 の色を順番にずらしてグラデーションが動くように見せる
			for i := 1; i <= 16; i++ {
				hue := float64((i*16 + offset) % 256) / 256.0
				r, g, b_color := hsvToRGB(hue, 1.0, 1.0)
				_ = b.Publish("vram", &bus.BusMessage{
					Target:    "set_palette",
					Operation: bus.OpCommand,
					Data:      []byte{byte(i), r, g, b_color},
					Source:    c.Name(),
				})
			}
			offset = (offset - 4) % 256
			if offset < 0 {
				offset += 256
			}
		}
	}
}

// 簡易的なHSV→RGB変換ヘルパー
func hsvToRGB(h, s, v float64) (byte, byte, byte) {
	i := int(h * 6)
	f := h*6 - float64(i)
	p := v * (1 - s)
	q := v * (1 - f*s)
	t := v * (1 - (1-f)*s)

	var r, g, b float64
	switch i % 6 {
	case 0: r, g, b = v, t, p
	case 1: r, g, b = q, v, p
	case 2: r, g, b = p, v, t
	case 3: r, g, b = p, q, v
	case 4: r, g, b = t, p, v
	case 5: r, g, b = v, p, q
	}
	return byte(r * 255), byte(g * 255), byte(b * 255)
}
