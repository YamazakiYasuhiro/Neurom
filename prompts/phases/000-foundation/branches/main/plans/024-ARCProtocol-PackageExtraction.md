# 024-ARCProtocol-PackageExtraction

> **Source Specification**: [024-ARCProtocol-PackageExtraction.md](file://prompts/phases/000-foundation/branches/main/ideas/024-ARCProtocol-PackageExtraction.md)

## Goal Description

バスコマンドのバイトレイアウト定義を `features/neurom/arcproto/` パッケージへ抽出し、
`internal/` の外に置くことで外部モジュールから import 可能にする。

現在バイトレイアウトの知識は 3 箇所に重複している。

| 箇所 | 役割 |
|---|---|
| `internal/modules/cpu/cpu.go` の `publish*` 系 12 関数 | エンコード（送信側） |
| `internal/modules/vram/vram.go` の `handle*` 系 | デコード（受信側） |
| `integration/*_test.go` の `blitIntegration` / `blitMultiCore` | エンコード（テスト用） |

これを `arcproto` に集約し、Encode と Decode を同一ファイル内で隣接させることで、
オフセット定数を共有し drift を構造的に防ぐ。

**本計画は振る舞いを一切変更しない純粋な内部リファクタリングである。**
生成されるバイト列が 1 ビットも変わらないことを、ゴールデンバイト列テストで機械的に保証する。

併せて、3 行のプレースホルダである `prompts/specifications/VRAM-Specification.md` を
実装ベースのプロトコル仕様書として書き起こす。

## User Review Required

以下 4 点は実装方針の分岐点であり、着手前に確認を頂きたい。

1. **`internal/modules/vram.BlendMode` を型エイリアスとして残すか**
   本計画では `type BlendMode = arcproto.BlendMode` の**エイリアス方式**を採用する。
   理由: `blend.go` の `BlendPixel(mode BlendMode, src, dst [4]uint8)` のシグネチャと、
   `integration/vram_multicore_test.go` が参照している `vram.BlendReplace` が無変更で済み、
   差分が最小化されて R6（振る舞い不変）の検証が容易になる。
   将来 `vram.BlendMode` を完全に廃止して `arcproto` へ一本化する場合は別計画とする。

2. **`publish*` のシグネチャを変えないこと**
   本計画では `cpu.go` の `publish*` 12 関数の**内側だけ**を `arcproto` 呼び出しに差し替え、
   引数の並びは現状のまま維持する。シーン関数（約 300 行）が無変更で済むため、
   R6 の検証範囲が「バイト列が同じか」だけに絞られる。
   `publish*` 自体の削除とシーンの書き換えは 027 で行う。

3. **プロトコル仕様書の置き場所**
   `prompts/specifications/VRAM-Specification.md`（既存スタブ）を埋める方針とする。
   ファイル名が VRAM 限定に見えるが、025 以降でバス全体のプロトコル（エンベロープ、トピック、
   バッチ）を追記していくため、`ARC-BusProtocol.md` への改名も選択肢である。
   本計画では**既存ファイル名を維持**し、改名は 028 の R10 に委ねる。

4. **`mode` コマンドの扱い**
   `cpu.go` 63-67 行は `Data: []byte{0x00, 0x01, 0x00}` を送るが、
   `vram.go` の `case "mode"` は Data を一切参照せずページ 0 を再初期化するだけである。
   R6（バイト列不変）を守るため、`arcproto.Mode{}.Encode()` は
   **この 3 バイトをそのまま返す**実装とし、意味付けは行わない。
   本来の意味（画面モード指定？）が判明した時点で別途仕様化が必要。

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| R1: `arcproto` パッケージの新設（`internal/` の外、`internal/bus` 非依存） | Proposed Changes > arcproto パッケージ（全ファイル）。検証は Verification Plan > 自動検証 1 |
| R2: コマンド・イベント・トピックの定数化 | Proposed Changes > `arcproto/target.go`, `arcproto/topic.go` |
| R3: コマンドごとの型と Encode / Decode の対称実装 | Proposed Changes > `arcproto/vram_draw.go`, `vram_palette.go`, `vram_page.go`, `vram_view.go`, `vram_read.go`, `event.go` |
| R4: 単位型の導入（Rotation / Scale / BlendMode / Color） | Proposed Changes > `arcproto/types.go` |
| R5: 既存呼び出し元の置き換え（vram / cpu / integration） | Proposed Changes > VRAM モジュール、CPU モジュール、統合テスト |
| R6: 振る舞いの完全な不変性 | Proposed Changes > `arcproto/golden_test.go`（R8 と統合）。Verification Plan > 自動検証 2, 3, 4 および 目視確認 |
| R7: プロトコル仕様書の作成 | Documentation > `VRAM-Specification.md` |
| R8: ゴールデンバイト列テスト | Proposed Changes > `arcproto/golden_test.go` |
| R9: ラウンドトリップテスト | Proposed Changes > `arcproto/roundtrip_test.go` |
| R10: 既存仕様書との齟齬の記録 | Documentation > `VRAM-Specification.md` の「既存仕様書との齟齬」節 |

**先送りする要件**: なし。R1〜R10 すべてを本計画で実装する。

## Proposed Changes

依存関係順（型 → プロトコル定義 → 呼び出し元）に並べる。
テストルール §2.1 に従い、各コンポーネントで `_test.go` を先に記述する。

---

### arcproto パッケージ（新規・公開）

#### [NEW] [features/neurom/arcproto/roundtrip_test.go](file://features/neurom/arcproto/roundtrip_test.go)

*   **Description**: R9。全コマンド・全イベントについて `Encode` → `Decode` で
    元の構造体が復元されることをテーブル駆動で検証する。**最初に書くテスト（TDD）**。
*   **Technical Design**:
    ```go
    package arcproto

    func TestRoundTripBlitRect(t *testing.T) {
        tests := []struct {
            name string
            in   BlitRect
        }{
            {"minimal", BlitRect{Page: 0, X: 0, Y: 0, W: 1, H: 1, Blend: BlendReplace, Pixels: []uint8{7}}},
            {"typical", BlitRect{Page: 1, X: 10, Y: 20, W: 4, H: 4, Blend: BlendAlpha, Pixels: make([]uint8, 16)}},
            {"max_u16", BlitRect{Page: 255, X: 65535, Y: 65535, W: 2, H: 2, Blend: BlendScreen, Pixels: []uint8{1, 2, 3, 4}}},
            {"large_512", BlitRect{Page: 0, X: 0, Y: 0, W: 512, H: 512, Blend: BlendReplace, Pixels: make([]uint8, 512*512)}},
        }
        for _, tt := range tests {
            t.Run(tt.name, func(t *testing.T) {
                got, err := DecodeBlitRect(tt.in.Encode())
                if err != nil {
                    t.Fatalf("DecodeBlitRect() error = %v", err)
                }
                if got.Page != tt.in.Page || got.X != tt.in.X || got.Y != tt.in.Y ||
                    got.W != tt.in.W || got.H != tt.in.H || got.Blend != tt.in.Blend {
                    t.Errorf("header mismatch: got %+v, want %+v", got, tt.in)
                }
                if !bytes.Equal(got.Pixels, tt.in.Pixels) {
                    t.Errorf("pixels mismatch")
                }
            })
        }
    }
    ```
*   **Logic**:
    *   同型のテスト関数を全 17 コマンド + 4 イベントに対して用意する。
    *   境界値ケースを必ず含める: ゼロ値、`u16` 最大値（65535）、`u8` 最大値（255）、
        512×512 の大容量ピクセル、`count = 0` のパレットブロック、
        `set_viewport` の負値（-1, -32768）。
    *   異常系: `Decode` に長さ不足のバイト列を渡し `ErrShortPayload` が返ることを検証する。

#### [NEW] [features/neurom/arcproto/golden_test.go](file://features/neurom/arcproto/golden_test.go)

*   **Description**: R8 / R6。既知の入力から**既知の固定バイト列**が生成されることを検証する。
    「1 ビットも変わらない」を機械的に保証する要。
*   **Technical Design**:
    ```go
    func TestGoldenEncode(t *testing.T) {
        tests := []struct {
            name string
            got  []byte
            want []byte
        }{
            {
                // cpu.go publishBlitRect(b, 0, 10, 20, 4, 4, 0, pixels) 相当
                name: "blit_rect",
                got:  BlitRect{Page: 0, X: 10, Y: 20, W: 4, H: 4, Blend: 0, Pixels: []uint8{1, 2, 3, 4}}.Encode(),
                want: []byte{
                    0x00,       // page
                    0x00, 0x0A, // x = 10
                    0x00, 0x14, // y = 20
                    0x00, 0x04, // w = 4
                    0x00, 0x04, // h = 4
                    0x00,       // blend = Replace
                    0x01, 0x02, 0x03, 0x04,
                },
            },
            {
                // cpu.go scene3Update の中央スプライト相当
                // publishBlitRectTransform(b, 0, 128, 106, 8, 8, 4, 4, rot, 0x0100, 0x0100, 0, sprite)
                name: "blit_rect_transform",
                got: BlitRectTransform{
                    Page: 0, X: 128, Y: 106, SrcW: 8, SrcH: 8,
                    PivotX: 4, PivotY: 4, Rotation: 64,
                    ScaleX: ScaleOne, ScaleY: ScaleOne, Blend: 0,
                    Pixels: make([]uint8, 64),
                }.Encode()[:19],
                want: []byte{
                    0x00,       // page
                    0x00, 0x80, // x = 128
                    0x00, 0x6A, // y = 106
                    0x00, 0x08, // srcW = 8
                    0x00, 0x08, // srcH = 8
                    0x00, 0x04, // pivotX = 4
                    0x00, 0x04, // pivotY = 4
                    0x40,       // rotation = 64
                    0x01, 0x00, // scaleX = 0x0100 (等倍)
                    0x01, 0x00, // scaleY = 0x0100
                    0x00,       // blend
                },
            },
            {
                // cpu.go 63-67 行の mode コマンド。意味付けせず 3 バイトをそのまま維持
                name: "mode_legacy_bytes",
                got:  Mode{}.Encode(),
                want: []byte{0x00, 0x01, 0x00},
            },
            {
                // cpu.go publishSetViewport(b, -1, -1) 相当。int16 の 2 の補数表現
                name: "set_viewport_negative",
                got:  SetViewport{OffX: -1, OffY: -1}.Encode(),
                want: []byte{0xFF, 0xFF, 0xFF, 0xFF},
            },
            {
                // cpu.go publishCopyRect(b, 0, 0, 0, 1, 0, 0, 256, 211) 相当（scene5Update）
                name: "copy_rect",
                got:  CopyRect{SrcPage: 0, DstPage: 0, SrcX: 0, SrcY: 1, DstX: 0, DstY: 0, W: 256, H: 211}.Encode(),
                want: []byte{
                    0x00,       // srcPage
                    0x00,       // dstPage
                    0x00, 0x00, // srcX
                    0x00, 0x01, // srcY
                    0x00, 0x00, // dstX
                    0x00, 0x00, // dstY
                    0x01, 0x00, // w = 256
                    0x00, 0xD3, // h = 211
                },
            },
        }
        for _, tt := range tests {
            t.Run(tt.name, func(t *testing.T) {
                if !bytes.Equal(tt.got, tt.want) {
                    t.Errorf("Encode() = % X, want % X", tt.got, tt.want)
                }
            })
        }
    }
    ```
*   **Logic**:
    *   全 17 コマンドについてゴールデンケースを用意する。
    *   期待値は**現行 `cpu.go` の `publish*` が生成するバイト列を手計算**して記述する。
        推測ではなく、現行コードのオフセット計算から導出すること。
    *   `set_palette_block` は `count = 3`（`scene2Init` 相当）と `count = 128`（`scene1Init` 相当）の
        2 ケースを含める。

#### [NEW] [features/neurom/arcproto/types_test.go](file://features/neurom/arcproto/types_test.go)

*   **Description**: R4。単位型の変換関数の境界値テスト。
*   **Technical Design**:
    ```go
    func TestDegrees(t *testing.T) {
        tests := []struct {
            name string
            deg  float64
            want Rotation
        }{
            {"zero", 0, 0},
            {"quarter", 90, 64},
            {"half", 180, 128},
            {"three_quarter", 270, 192},
            {"full_wraps_to_zero", 360, 0},
            {"over_full", 450, 64},
            {"negative", -90, 192},
        }
        // ...
    }

    func TestScaleOf(t *testing.T) {
        tests := []struct {
            name string
            f    float64
            want Scale
        }{
            {"one", 1.0, 0x0100},
            {"two", 2.0, 0x0200},
            {"half", 0.5, 0x0080},
            {"zero_clamps_to_one", 0, 0x0100},
            {"negative_clamps_to_one", -1.0, 0x0100},
        }
        // ...
    }
    ```
*   **Logic**:
    *   `Rotation` は 0-255 で一周（`transform.go` 31 行が `float64(rotation)/256.0*2π` で使用）。
        よって `Degrees(deg) = Rotation(int(math.Round(deg/360.0*256.0)) & 0xFF)`。
        負値も `& 0xFF` で正しく巻き戻る。
    *   `Scale` は 8.8 固定小数点（`transform.go` 35-36 行が `float64(scaleX)/256.0` で使用）。
        `ScaleOf(f) = Scale(math.Round(f * 256.0))`。
        `transform.go` 38-43 行は `sx <= 0` を 1.0 に丸めるため、
        `ScaleOf` も 0 以下を `ScaleOne` に丸めて挙動を一致させる。

#### [NEW] [features/neurom/arcproto/doc.go](file://features/neurom/arcproto/doc.go)

*   **Description**: パッケージドキュメント。プロトコルの概要と設計制約を記述する。
*   **Logic**:
    *   全整数はビッグエンディアンであること。
    *   このパッケージは `internal/bus` を import してはならず、
        バイト列の生成・解析のみを責務とし `Publish` を行わないこと（R1）。
    *   Encode と Decode はオフセット定数を共有し、同一ファイル内に隣接して置くこと（R3）。

#### [NEW] [features/neurom/arcproto/topic.go](file://features/neurom/arcproto/topic.go)

*   **Description**: R2。トピック名の定数化。
*   **Technical Design**:
    ```go
    package arcproto

    // トピック名。現行の文字列をそのまま定数化する（025 で階層命名へ改称予定）。
    const (
        TopicVRAM          = "vram"
        TopicVRAMUpdate    = "vram_update"
        TopicMonitor       = "monitor"
        TopicMonitorUpdate = "monitor_update"
        TopicSystem        = "system"
        TopicIO            = "io"
    )
    ```
*   **Logic**:
    *   値は現行コードの文字列と**完全一致**させる（R6）。
    *   `internal/bus/message.go` 20-22 行の `TargetSystem = "System"` / `CmdShutdown = "Shutdown"` は
        バス層の制御用であり、本計画では `arcproto` へ移さず `internal/bus` に残す。

#### [NEW] [features/neurom/arcproto/target.go](file://features/neurom/arcproto/target.go)

*   **Description**: R2。コマンドおよびイベントの Target 文字列を定数化する。
*   **Technical Design**:
    ```go
    package arcproto

    // コマンド Target。値は internal/modules/vram/vram.go の handleMessage の
    // switch 分岐と 1:1 で一致させる。
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

    // イベント Target。VRAM が発行する。
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

    // ページ関連のエラーコード。vram.go の publishPageError の引数値に対応する。
    const (
        PageErrInvalidPage    uint8 = 0x01 // 無効なページ番号
        PageErrInvalidDisplay uint8 = 0x03 // 無効な表示ページ指定
    )
    ```
*   **Logic**:
    *   `PageErrInvalidPage` / `PageErrInvalidDisplay` の値は `vram.go` の
        `v.publishPageError(0x01)` および `v.publishPageError(0x03)` の呼び出しから抽出する。
        `0x02` は現行実装に存在しないため定義しない（仕様書に「欠番」として記録する）。

#### [NEW] [features/neurom/arcproto/types.go](file://features/neurom/arcproto/types.go)

*   **Description**: R4。単位型と共通エラーの定義。
*   **Technical Design**:
    ```go
    package arcproto

    import (
        "errors"
        "math"
    )

    var (
        ErrShortPayload = errors.New("arcproto: payload too short")
        ErrBadPayload   = errors.New("arcproto: malformed payload")
    )

    // Rotation は 0-255 で一周を表す回転量。
    // internal/modules/vram/transform.go が float64(rotation)/256.0*2π として解釈する。
    type Rotation uint8

    // Degrees は度数から Rotation を生成する。360 度で巻き戻り、負値も正しく扱う。
    func Degrees(deg float64) Rotation {
        return Rotation(int(math.Round(deg/360.0*256.0)) & 0xFF)
    }

    // Turns は回転数から Rotation を生成する。1.0 が一周。
    func Turns(t float64) Rotation {
        return Rotation(int(math.Round(t*256.0)) & 0xFF)
    }

    // Scale は 8.8 固定小数点の拡大率。ScaleOne が等倍。
    // internal/modules/vram/transform.go が float64(scale)/256.0 として解釈する。
    type Scale uint16

    const ScaleOne Scale = 0x0100

    // ScaleOf は倍率から Scale を生成する。
    // 0 以下は transform.go の挙動（sx <= 0 を 1.0 に丸める）に合わせて ScaleOne とする。
    func ScaleOf(f float64) Scale {
        if f <= 0 {
            return ScaleOne
        }
        return Scale(math.Round(f * 256.0))
    }

    // BlendMode は合成モード。値は internal/modules/vram/blend.go と一致させる。
    type BlendMode uint8

    const (
        BlendReplace  BlendMode = 0x00
        BlendAlpha    BlendMode = 0x01
        BlendAdditive BlendMode = 0x02
        BlendMultiply BlendMode = 0x03
        BlendScreen   BlendMode = 0x04
    )

    // Color は RGBA パレット色。
    type Color struct{ R, G, B, A uint8 }
    ```
*   **Logic**:
    *   `BlendMode` の値は `blend.go` 6-17 行の定義（Replace 0x00, Alpha 0x01, Additive 0x02,
        Multiply 0x03, Screen 0x04）と厳密に一致させる。ずれると描画結果が変わる。

#### [NEW] [features/neurom/arcproto/vram_draw.go](file://features/neurom/arcproto/vram_draw.go)

*   **Description**: R3。描画系コマンド（`mode`, `draw_pixel`, `clear_vram`, `blit_rect`,
    `blit_rect_transform`, `copy_rect`）の型と Encode / Decode。
*   **Technical Design**:
    ```go
    package arcproto

    import "encoding/binary"

    // --- mode ---
    // 現行実装（vram.go の case "mode"）は Data を参照せずページ 0 を再初期化する。
    // cpu.go 63-67 行が送る 3 バイトを R6 のためそのまま維持する。
    type Mode struct{}

    func (c Mode) Target() string { return TargetMode }
    func (c Mode) Encode() []byte { return []byte{0x00, 0x01, 0x00} }

    // --- draw_pixel ---
    // レイアウト: [page:u8][x:u16][y:u16][p:u8] = 6 bytes
    type DrawPixel struct {
        Page uint8
        X, Y uint16
        P    uint8
    }

    const drawPixelLen = 6

    func (c DrawPixel) Target() string { return TargetDrawPixel }

    func (c DrawPixel) Encode() []byte {
        d := make([]byte, drawPixelLen)
        d[0] = c.Page
        binary.BigEndian.PutUint16(d[1:], c.X)
        binary.BigEndian.PutUint16(d[3:], c.Y)
        d[5] = c.P
        return d
    }

    func DecodeDrawPixel(data []byte) (DrawPixel, error) {
        if len(data) < drawPixelLen {
            return DrawPixel{}, ErrShortPayload
        }
        return DrawPixel{
            Page: data[0],
            X:    binary.BigEndian.Uint16(data[1:]),
            Y:    binary.BigEndian.Uint16(data[3:]),
            P:    data[5],
        }, nil
    }

    // --- clear_vram ---
    // レイアウト: [page:u8][palette_idx:u8] = 2 bytes
    // 現行の vram.go は len>=1 で page、len>=2 で palette_idx を読み、
    // 不足分は 0 を既定値とする。Decode もこの寛容さを維持する。
    type ClearVRAM struct {
        Page       uint8
        PaletteIdx uint8
    }

    const clearVRAMLen = 2

    func (c ClearVRAM) Target() string { return TargetClearVRAM }

    func (c ClearVRAM) Encode() []byte {
        return []byte{c.Page, c.PaletteIdx}
    }

    // DecodeClearVRAM は現行実装の寛容な解釈を再現する。
    // 空 payload でもエラーとせず、ゼロ値を返す。
    func DecodeClearVRAM(data []byte) (ClearVRAM, error) {
        var c ClearVRAM
        if len(data) >= 1 {
            c.Page = data[0]
        }
        if len(data) >= 2 {
            c.PaletteIdx = data[1]
        }
        return c, nil
    }

    // --- blit_rect ---
    // レイアウト: [page:u8][x:u16][y:u16][w:u16][h:u16][blend:u8][pixels...]
    type BlitRect struct {
        Page   uint8
        X, Y   uint16
        W, H   uint16
        Blend  BlendMode
        Pixels []uint8
    }

    const blitRectHeaderLen = 10

    func (c BlitRect) Target() string { return TargetBlitRect }

    func (c BlitRect) Encode() []byte {
        d := make([]byte, blitRectHeaderLen+len(c.Pixels))
        d[0] = c.Page
        binary.BigEndian.PutUint16(d[1:], c.X)
        binary.BigEndian.PutUint16(d[3:], c.Y)
        binary.BigEndian.PutUint16(d[5:], c.W)
        binary.BigEndian.PutUint16(d[7:], c.H)
        d[9] = uint8(c.Blend)
        copy(d[blitRectHeaderLen:], c.Pixels)
        return d
    }

    func DecodeBlitRect(data []byte) (BlitRect, error) {
        if len(data) < blitRectHeaderLen {
            return BlitRect{}, ErrShortPayload
        }
        return BlitRect{
            Page:   data[0],
            X:      binary.BigEndian.Uint16(data[1:]),
            Y:      binary.BigEndian.Uint16(data[3:]),
            W:      binary.BigEndian.Uint16(data[5:]),
            H:      binary.BigEndian.Uint16(data[7:]),
            Blend:  BlendMode(data[9]),
            Pixels: data[blitRectHeaderLen:],
        }, nil
    }

    // --- blit_rect_transform ---
    // レイアウト:
    //   [page:u8][x:u16][y:u16][srcW:u16][srcH:u16]
    //   [pivotX:u16][pivotY:u16][rotation:u8][scaleX:u16][scaleY:u16]
    //   [blend:u8][pixels...]
    type BlitRectTransform struct {
        Page           uint8
        X, Y           uint16
        SrcW, SrcH     uint16
        PivotX, PivotY uint16
        Rotation       Rotation
        ScaleX, ScaleY Scale
        Blend          BlendMode
        Pixels         []uint8
    }

    const blitRectTransformHeaderLen = 19

    func (c BlitRectTransform) Target() string { return TargetBlitRectTransform }

    func (c BlitRectTransform) Encode() []byte {
        d := make([]byte, blitRectTransformHeaderLen+len(c.Pixels))
        d[0] = c.Page
        binary.BigEndian.PutUint16(d[1:], c.X)
        binary.BigEndian.PutUint16(d[3:], c.Y)
        binary.BigEndian.PutUint16(d[5:], c.SrcW)
        binary.BigEndian.PutUint16(d[7:], c.SrcH)
        binary.BigEndian.PutUint16(d[9:], c.PivotX)
        binary.BigEndian.PutUint16(d[11:], c.PivotY)
        d[13] = uint8(c.Rotation)
        binary.BigEndian.PutUint16(d[14:], uint16(c.ScaleX))
        binary.BigEndian.PutUint16(d[16:], uint16(c.ScaleY))
        d[18] = uint8(c.Blend)
        copy(d[blitRectTransformHeaderLen:], c.Pixels)
        return d
    }

    func DecodeBlitRectTransform(data []byte) (BlitRectTransform, error) {
        if len(data) < blitRectTransformHeaderLen {
            return BlitRectTransform{}, ErrShortPayload
        }
        return BlitRectTransform{
            Page:     data[0],
            X:        binary.BigEndian.Uint16(data[1:]),
            Y:        binary.BigEndian.Uint16(data[3:]),
            SrcW:     binary.BigEndian.Uint16(data[5:]),
            SrcH:     binary.BigEndian.Uint16(data[7:]),
            PivotX:   binary.BigEndian.Uint16(data[9:]),
            PivotY:   binary.BigEndian.Uint16(data[11:]),
            Rotation: Rotation(data[13]),
            ScaleX:   Scale(binary.BigEndian.Uint16(data[14:])),
            ScaleY:   Scale(binary.BigEndian.Uint16(data[16:])),
            Blend:    BlendMode(data[18]),
            Pixels:   data[blitRectTransformHeaderLen:],
        }, nil
    }

    // --- copy_rect ---
    // レイアウト: [srcPage:u8][dstPage:u8][srcX:u16][srcY:u16][dstX:u16][dstY:u16][w:u16][h:u16] = 14 bytes
    type CopyRect struct {
        SrcPage, DstPage uint8
        SrcX, SrcY       uint16
        DstX, DstY       uint16
        W, H             uint16
    }

    const copyRectLen = 14

    func (c CopyRect) Target() string { return TargetCopyRect }

    func (c CopyRect) Encode() []byte {
        d := make([]byte, copyRectLen)
        d[0] = c.SrcPage
        d[1] = c.DstPage
        binary.BigEndian.PutUint16(d[2:], c.SrcX)
        binary.BigEndian.PutUint16(d[4:], c.SrcY)
        binary.BigEndian.PutUint16(d[6:], c.DstX)
        binary.BigEndian.PutUint16(d[8:], c.DstY)
        binary.BigEndian.PutUint16(d[10:], c.W)
        binary.BigEndian.PutUint16(d[12:], c.H)
        return d
    }

    func DecodeCopyRect(data []byte) (CopyRect, error) {
        if len(data) < copyRectLen {
            return CopyRect{}, ErrShortPayload
        }
        return CopyRect{
            SrcPage: data[0],
            DstPage: data[1],
            SrcX:    binary.BigEndian.Uint16(data[2:]),
            SrcY:    binary.BigEndian.Uint16(data[4:]),
            DstX:    binary.BigEndian.Uint16(data[6:]),
            DstY:    binary.BigEndian.Uint16(data[8:]),
            W:       binary.BigEndian.Uint16(data[10:]),
            H:       binary.BigEndian.Uint16(data[12:]),
        }, nil
    }
    ```
*   **Logic**:
    *   `Pixels` は `Decode` 時に**元データのサブスライスを返す**（コピーしない）。
        現行の `vram.go` も `pixelData := msg.Data[10:]` としてサブスライスを使うため、
        挙動が一致し、かつ確保も発生しない。
    *   `blit_rect` / `blit_rect_transform` の `len(pixelData) < w*h` チェックは
        **`arcproto` では行わない**。現行の `vram.go` 347-349 行 / 413-415 行が
        クリッピング前に検査しているため、この責務は VRAM 側に残す。

#### [NEW] [features/neurom/arcproto/vram_palette.go](file://features/neurom/arcproto/vram_palette.go)

*   **Description**: R3。パレット系コマンド（`set_palette`, `set_palette_block`,
    `read_palette_block`）の型と Encode / Decode。
*   **Technical Design**:
    ```go
    // --- set_palette ---
    // レイアウト(4 bytes): [index:u8][R:u8][G:u8][B:u8]  ※ alpha は 255 既定
    // レイアウト(5 bytes): [index:u8][R:u8][G:u8][B:u8][A:u8]
    type SetPalette struct {
        Index uint8
        Color Color
        // OmitAlpha を true にすると 4 バイト形式でエンコードする（後方互換の再現用）。
        OmitAlpha bool
    }

    func (c SetPalette) Target() string { return TargetSetPalette }

    func (c SetPalette) Encode() []byte {
        if c.OmitAlpha {
            return []byte{c.Index, c.Color.R, c.Color.G, c.Color.B}
        }
        return []byte{c.Index, c.Color.R, c.Color.G, c.Color.B, c.Color.A}
    }

    func DecodeSetPalette(data []byte) (SetPalette, error) {
        if len(data) < 4 {
            return SetPalette{}, ErrShortPayload
        }
        c := SetPalette{
            Index: data[0],
            Color: Color{R: data[1], G: data[2], B: data[3], A: 255},
        }
        if len(data) >= 5 {
            c.Color.A = data[4]
        } else {
            c.OmitAlpha = true
        }
        return c, nil
    }

    // --- set_palette_block ---
    // レイアウト: [start:u8][count:u8][R:u8][G:u8][B:u8][A:u8] × count
    type SetPaletteBlock struct {
        Start  uint8
        Colors []Color
    }

    const paletteBlockHeaderLen = 2

    func (c SetPaletteBlock) Target() string { return TargetSetPaletteBlock }

    func (c SetPaletteBlock) Encode() []byte {
        count := len(c.Colors)
        d := make([]byte, paletteBlockHeaderLen+count*4)
        d[0] = c.Start
        d[1] = uint8(count)
        for i, col := range c.Colors {
            off := paletteBlockHeaderLen + i*4
            d[off] = col.R
            d[off+1] = col.G
            d[off+2] = col.B
            d[off+3] = col.A
        }
        return d
    }

    func DecodeSetPaletteBlock(data []byte) (SetPaletteBlock, error) {
        if len(data) < paletteBlockHeaderLen {
            return SetPaletteBlock{}, ErrShortPayload
        }
        start := data[0]
        count := int(data[1])
        if len(data) < paletteBlockHeaderLen+count*4 {
            return SetPaletteBlock{}, ErrShortPayload
        }
        colors := make([]Color, count)
        for i := range count {
            off := paletteBlockHeaderLen + i*4
            colors[i] = Color{R: data[off], G: data[off+1], B: data[off+2], A: data[off+3]}
        }
        return SetPaletteBlock{Start: start, Colors: colors}, nil
    }

    // --- read_palette_block ---
    // レイアウト: [start:u8][count:u8] = 2 bytes
    type ReadPaletteBlock struct {
        Start uint8
        Count uint8
    }

    func (c ReadPaletteBlock) Target() string { return TargetReadPaletteBlock }
    func (c ReadPaletteBlock) Encode() []byte { return []byte{c.Start, c.Count} }

    func DecodeReadPaletteBlock(data []byte) (ReadPaletteBlock, error) {
        if len(data) < 2 {
            return ReadPaletteBlock{}, ErrShortPayload
        }
        return ReadPaletteBlock{Start: data[0], Count: data[1]}, nil
    }
    ```
*   **Logic**:
    *   `Encode` の `d[1] = uint8(count)` は `len(c.Colors)` が 256 以上のときに切り詰まる。
        現行の `cpu.go` は最大 128 色ずつ 2 回に分けて送っている（`scene1Init` 155-158 行）ため
        実害はないが、`Encode` 時に 256 を超える場合の扱いを godoc に明記する。
    *   `SetPalette.OmitAlpha` は既存の 4 バイト形式を再現するために必要。
        現行 `cpu.go` は `set_palette` を使っていない（`set_palette_block` のみ）が、
        `integration/palette_test.go` と `integration/vram_multicore_test.go` が使用している。

#### [NEW] [features/neurom/arcproto/vram_page.go](file://features/neurom/arcproto/vram_page.go)

*   **Description**: R3。ページ系コマンド（`set_page_count`, `set_display_page`,
    `swap_pages`, `copy_page`, `set_page_size`）の型と Encode / Decode。
*   **Technical Design**:
    ```go
    // --- set_page_count ---
    // レイアウト: [count:u8]  ※ 0 は 256 を意味する（vram.go 619-621 行）
    type SetPageCount struct{ Count uint8 }

    func (c SetPageCount) Target() string { return TargetSetPageCount }
    func (c SetPageCount) Encode() []byte { return []byte{c.Count} }

    func DecodeSetPageCount(data []byte) (SetPageCount, error) {
        if len(data) < 1 {
            return SetPageCount{}, ErrShortPayload
        }
        return SetPageCount{Count: data[0]}, nil
    }

    // ResolvedCount は 0 を 256 と解釈した実際のページ数を返す。
    func (c SetPageCount) ResolvedCount() int {
        if c.Count == 0 {
            return 256
        }
        return int(c.Count)
    }

    // --- set_display_page ---
    type SetDisplayPage struct{ Page uint8 }
    // Encode: []byte{c.Page} / Decode: len < 1 でエラー

    // --- swap_pages ---
    type SwapPages struct{ Page1, Page2 uint8 }
    // Encode: []byte{c.Page1, c.Page2} / Decode: len < 2 でエラー

    // --- copy_page ---
    type CopyPage struct{ Src, Dst uint8 }
    // Encode: []byte{c.Src, c.Dst} / Decode: len < 2 でエラー

    // --- set_page_size ---
    // レイアウト: [page:u8][w:u16][h:u16] = 5 bytes
    type SetPageSize struct {
        Page uint8
        W, H uint16
    }

    const setPageSizeLen = 5

    func (c SetPageSize) Encode() []byte {
        d := make([]byte, setPageSizeLen)
        d[0] = c.Page
        binary.BigEndian.PutUint16(d[1:], c.W)
        binary.BigEndian.PutUint16(d[3:], c.H)
        return d
    }
    ```
*   **Logic**:
    *   `ResolvedCount()` は `vram.go` 619-621 行の `if count == 0 { count = 256 }` を
        プロトコル層のヘルパーとして提供する。VRAM 側はこれを呼ぶようにする。
    *   `SetDisplayPage` / `SwapPages` / `CopyPage` も同じパターンで Encode / Decode を実装する
        （紙面短縮のため上記では省略記法にしているが、実装では全て明示的に書くこと）。

#### [NEW] [features/neurom/arcproto/vram_view.go](file://features/neurom/arcproto/vram_view.go)

*   **Description**: R3。`set_viewport` の型と Encode / Decode。**符号付き整数を扱う唯一のコマンド**。
*   **Technical Design**:
    ```go
    // --- set_viewport ---
    // レイアウト: [offX:i16][offY:i16] = 4 bytes（ビッグエンディアン、2 の補数）
    type SetViewport struct {
        OffX, OffY int16
    }

    const setViewportLen = 4

    func (c SetViewport) Target() string { return TargetSetViewport }

    func (c SetViewport) Encode() []byte {
        d := make([]byte, setViewportLen)
        binary.BigEndian.PutUint16(d[0:], uint16(c.OffX))
        binary.BigEndian.PutUint16(d[2:], uint16(c.OffY))
        return d
    }

    func DecodeSetViewport(data []byte) (SetViewport, error) {
        if len(data) < setViewportLen {
            return SetViewport{}, ErrShortPayload
        }
        return SetViewport{
            OffX: int16(binary.BigEndian.Uint16(data[0:])),
            OffY: int16(binary.BigEndian.Uint16(data[2:])),
        }, nil
    }
    ```
*   **Logic**:
    *   `uint16(c.OffX)` / `int16(...)` の変換は現行の
        `cpu.go` 237-245 行と `vram.go` 724-734 行と同一であり、
        Go の変換規則により 2 の補数表現が保たれる。
    *   ラウンドトリップテストで -1 / -32768 / 32767 を必ず検証する。

#### [NEW] [features/neurom/arcproto/vram_read.go](file://features/neurom/arcproto/vram_read.go)

*   **Description**: R3。読み出しコマンド `read_rect`、`get_stats` と、
    そのレスポンス `rect_data` / `palette_data` の型。
*   **Technical Design**:
    ```go
    // --- read_rect ---
    // レイアウト: [page:u8][x:u16][y:u16][w:u16][h:u16] = 9 bytes
    type ReadRect struct {
        Page uint8
        X, Y uint16
        W, H uint16
    }

    const readRectLen = 9

    // --- get_stats ---
    // payload を持たない。
    type GetStats struct{}

    func (c GetStats) Target() string { return TargetGetStats }
    func (c GetStats) Encode() []byte { return nil }

    // --- rect_data (レスポンス) ---
    // レイアウト: [x:u16][y:u16][w:u16][h:u16][pixels: w*h bytes]
    // 注意: page は含まれない（vram.go 480-485 行）
    type RectData struct {
        X, Y   uint16
        W, H   uint16
        Pixels []uint8
    }

    const rectDataHeaderLen = 8

    func (e RectData) Target() string { return EventRectData }

    func (e RectData) Encode() []byte {
        d := make([]byte, rectDataHeaderLen+len(e.Pixels))
        binary.BigEndian.PutUint16(d[0:], e.X)
        binary.BigEndian.PutUint16(d[2:], e.Y)
        binary.BigEndian.PutUint16(d[4:], e.W)
        binary.BigEndian.PutUint16(d[6:], e.H)
        copy(d[rectDataHeaderLen:], e.Pixels)
        return d
    }

    func DecodeRectData(data []byte) (RectData, error) {
        if len(data) < rectDataHeaderLen {
            return RectData{}, ErrShortPayload
        }
        return RectData{
            X:      binary.BigEndian.Uint16(data[0:]),
            Y:      binary.BigEndian.Uint16(data[2:]),
            W:      binary.BigEndian.Uint16(data[4:]),
            H:      binary.BigEndian.Uint16(data[6:]),
            Pixels: data[rectDataHeaderLen:],
        }, nil
    }

    // --- palette_data (レスポンス) ---
    // レイアウト: [start:u8][count:u8][R][G][B][A] × count
    // set_palette_block と同一レイアウトのため、そのエンコーダを再利用する。
    type PaletteData = SetPaletteBlock
    ```
*   **Logic**:
    *   `rect_data` に `page` が含まれない点は現行実装（`vram.go` 480-485 行）どおり。
        **仕様書に「読み出し元ページが応答に含まれないため、要求元が自分で覚えておく必要がある」
        という制約として明記する**（025 の相関 ID 導入時に解決される課題）。
    *   `PaletteData` は `SetPaletteBlock` の型エイリアスとする。
        `vram.go` 591-605 行が生成する `resp` のレイアウトは
        `set_palette_block` の payload と完全に同一である。

#### [NEW] [features/neurom/arcproto/event.go](file://features/neurom/arcproto/event.go)

*   **Description**: R3。`rect_updated` と `page_error` の型。
    その他のイベントはコマンド payload のエコーであるため、対応するコマンド型を再利用する。
*   **Technical Design**:
    ```go
    // --- rect_updated ---
    // レイアウト: [x:u16][y:u16][w:u16][h:u16] = 8 bytes
    // 注意: vram.go 739-743 行は int を uint16 に切り詰めて格納する。
    // 負の x/y は 2 の補数で uint16 に丸められるため、大きな正値として現れる。
    type RectUpdated struct {
        X, Y uint16
        W, H uint16
    }

    const rectUpdatedLen = 8

    func (e RectUpdated) Target() string { return EventRectUpdated }

    func (e RectUpdated) Encode() []byte {
        d := make([]byte, rectUpdatedLen)
        binary.BigEndian.PutUint16(d[0:], e.X)
        binary.BigEndian.PutUint16(d[2:], e.Y)
        binary.BigEndian.PutUint16(d[4:], e.W)
        binary.BigEndian.PutUint16(d[6:], e.H)
        return d
    }

    func DecodeRectUpdated(data []byte) (RectUpdated, error) {
        if len(data) < rectUpdatedLen {
            return RectUpdated{}, ErrShortPayload
        }
        return RectUpdated{
            X: binary.BigEndian.Uint16(data[0:]),
            Y: binary.BigEndian.Uint16(data[2:]),
            W: binary.BigEndian.Uint16(data[4:]),
            H: binary.BigEndian.Uint16(data[6:]),
        }, nil
    }

    // --- page_error ---
    // レイアウト: [code:u8] = 1 byte
    type PageError struct{ Code uint8 }

    func (e PageError) Target() string { return EventPageError }
    func (e PageError) Encode() []byte { return []byte{e.Code} }

    func DecodePageError(data []byte) (PageError, error) {
        if len(data) < 1 {
            return PageError{}, ErrShortPayload
        }
        return PageError{Code: data[0]}, nil
    }
    ```
*   **Logic**:
    *   エコー型イベントの対応表を godoc に記載する。
        `vram_updated` は `draw_pixel` の payload、
        `vram_cleared` は `clear_vram` の 2 バイト、
        `palette_updated` は `set_palette` の payload、
        `palette_block_updated` は `set_palette_block` の先頭 2 バイトのみ、
        `pages_swapped` / `page_copied` は該当コマンドの先頭 2 バイト、
        `page_size_changed` は先頭 5 バイト、
        `viewport_changed` は先頭 4 バイト、
        `rect_copied` は `copy_rect` の payload 全体、
        `page_count_changed` は 1 バイト、
        `display_page_changed` は 1 バイト、
        `mode_changed` は payload なし。

---

### VRAM モジュール（既存の変更）

#### [MODIFY] [features/neurom/internal/modules/vram/blend.go](file://features/neurom/internal/modules/vram/blend.go)

*   **Description**: `BlendMode` と 5 つの定数を `arcproto` への型エイリアスに置き換える。
    `BlendPixel` の実装本体は変更しない。
*   **Technical Design**:
    ```go
    package vram

    import "github.com/axsh/neurom/arcproto"

    // BlendMode は arcproto の定義を再輸出する。
    // 既存の呼び出し元（blend_test.go, integration/vram_multicore_test.go）を
    // 無変更で維持するための型エイリアス。
    type BlendMode = arcproto.BlendMode

    const (
        BlendReplace  = arcproto.BlendReplace
        BlendAlpha    = arcproto.BlendAlpha
        BlendAdditive = arcproto.BlendAdditive
        BlendMultiply = arcproto.BlendMultiply
        BlendScreen   = arcproto.BlendScreen
    )

    // BlendPixel 以下は無変更
    ```
*   **Logic**:
    *   **型エイリアス（`=`）であることが重要**。`type BlendMode arcproto.BlendMode` の
        定義型にすると `integration/vram_multicore_test.go` 70 行の
        `blitMultiCore(..., vram.BlendReplace, ...)` で型不一致が起きる。

#### [MODIFY] [features/neurom/internal/modules/vram/vram.go](file://features/neurom/internal/modules/vram/vram.go)

*   **Description**: R5。`handleMessage` の `switch` を `arcproto` の Target 定数に、
    各 `handle*` の手書きオフセット解析を `arcproto` の Decode に置き換える。
    イベント発行の Target とトピックも定数参照にする。
*   **Technical Design**:
    ```go
    // 置換パターン（handleBlitRect の例）
    // Before:
    func (v *VRAMModule) handleBlitRect(msg *bus.BusMessage) {
        if len(msg.Data) < 10 {
            return
        }
        page := int(msg.Data[0])
        if !v.isValidPage(page) {
            v.publishPageError(0x01)
            return
        }
        pg := &v.pages[page]
        dstX := int(binary.BigEndian.Uint16(msg.Data[1:]))
        dstY := int(binary.BigEndian.Uint16(msg.Data[3:]))
        w := int(binary.BigEndian.Uint16(msg.Data[5:]))
        h := int(binary.BigEndian.Uint16(msg.Data[7:]))
        blendMode := BlendMode(msg.Data[9])
        pixelData := msg.Data[10:]
        if len(pixelData) < w*h {
            return
        }
        // ... 以降のクリッピングと描画は無変更
    }

    // After:
    func (v *VRAMModule) handleBlitRect(msg *bus.BusMessage) {
        c, err := arcproto.DecodeBlitRect(msg.Data)
        if err != nil {
            return // 現行の「黙って return」を維持（R6）
        }
        page := int(c.Page)
        if !v.isValidPage(page) {
            v.publishPageError(arcproto.PageErrInvalidPage)
            return
        }
        pg := &v.pages[page]
        dstX, dstY := int(c.X), int(c.Y)
        w, h := int(c.W), int(c.H)
        blendMode := c.Blend
        pixelData := c.Pixels
        if len(pixelData) < w*h {
            return
        }
        // ... 以降のクリッピングと描画は無変更
    }
    ```
*   **Logic**:
    *   **置換順序が重要**: `handle*` を 1 つずつ置換し、都度 `build.sh` を実行する。
        まとめて置換すると R6 違反の原因特定が困難になる。
    *   置換対象の全 16 ハンドラ:
        `handleDrawPixel`, `handleSetPalette`, `handleClearVRAM`, `handleBlitRect`,
        `handleBlitRectTransform`, `handleReadRect`, `handleCopyRect`,
        `handleSetPaletteBlock`, `handleReadPaletteBlock`, `handleSetPageCount`,
        `handleSetDisplayPage`, `handleSwapPages`, `handleCopyPage`,
        `handleSetPageSize`, `handleSetViewport`、および `case "mode"` のインライン処理。
    *   `handleSetPageCount` は `count := c.ResolvedCount()` を用い、
        現行の `if count == 0 { count = 256 }` を削除する。
    *   `handleReadRect` のレスポンス生成を `arcproto.RectData{...}.Encode()` に置き換える。
        ただし**ピクセル読み出しループ自体は無変更**（`vram.go` 486-495 行）。
    *   `handleReadPaletteBlock` のレスポンス生成を `arcproto.PaletteData{...}.Encode()` に置き換える。
    *   イベント発行の `v.bus.Publish("vram_update", ...)` を
        `v.bus.Publish(arcproto.TopicVRAMUpdate, ...)` に、
        `Target: "rect_updated"` を `Target: arcproto.EventRectUpdated` に置き換える。
    *   `publishPageError` の引数を `arcproto.PageErrInvalidPage` /
        `arcproto.PageErrInvalidDisplay` に置き換える。
    *   `Start` メソッドの `b.Subscribe("vram")` を `b.Subscribe(arcproto.TopicVRAM)` に、
        `b.Subscribe("system")` を `b.Subscribe(arcproto.TopicSystem)` に置き換える。
    *   置換完了後、`vram.go` の import から `encoding/binary` が不要になるはずである
        （不要にならない場合は置換漏れがある）。

---

### CPU モジュール（既存の変更）

#### [MODIFY] [features/neurom/internal/modules/cpu/cpu.go](file://features/neurom/internal/modules/cpu/cpu.go)

*   **Description**: R5。`publish*` 12 関数の**内側だけ**を `arcproto` の Encode に置き換える。
    シグネチャとシーン関数（247-520 行）は一切変更しない。
*   **Technical Design**:
    ```go
    // Before
    func (c *CPUModule) publishBlitRect(b bus.Bus, page uint8, x, y, w, h uint16, blendMode uint8, pixels []uint8) {
        data := make([]byte, 10+len(pixels))
        data[0] = page
        binary.BigEndian.PutUint16(data[1:], x)
        binary.BigEndian.PutUint16(data[3:], y)
        binary.BigEndian.PutUint16(data[5:], w)
        binary.BigEndian.PutUint16(data[7:], h)
        data[9] = blendMode
        copy(data[10:], pixels)
        b.Publish("vram", &bus.BusMessage{
            Target: "blit_rect", Operation: bus.OpCommand,
            Data: data, Source: c.Name(),
        })
    }

    // After — シグネチャは不変
    func (c *CPUModule) publishBlitRect(b bus.Bus, page uint8, x, y, w, h uint16, blendMode uint8, pixels []uint8) {
        c.publishCmd(b, arcproto.BlitRect{
            Page: page, X: x, Y: y, W: w, H: h,
            Blend: arcproto.BlendMode(blendMode), Pixels: pixels,
        })
    }

    // command は Target と Encode を持つ arcproto のコマンド型を受ける。
    type command interface {
        Target() string
        Encode() []byte
    }

    // publishCmd は全 publish* が共通で使う送信ヘルパー。
    func (c *CPUModule) publishCmd(b bus.Bus, cmd command) {
        b.Publish(arcproto.TopicVRAM, &bus.BusMessage{
            Target:    cmd.Target(),
            Operation: bus.OpCommand,
            Data:      cmd.Encode(),
            Source:    c.Name(),
        })
    }
    ```
*   **Logic**:
    *   `publishCmd` の導入により 12 関数それぞれの `b.Publish(...)` ボイラープレートが消える。
    *   `publishSetPaletteBlock(b, start, count uint8, colors [][4]uint8)` は
        シグネチャを維持したまま、内部で `[][4]uint8` → `[]arcproto.Color` へ変換する。
        `count` 引数を尊重して `colors[:count]` を渡すこと
        （現行実装は `for i := range int(count)` でループしており、
        `len(colors) > count` の場合は `count` 個しか送らない）。
    *   `run` メソッド 63-67 行の `mode` 送信を `c.publishCmd(b, arcproto.Mode{})` に置き換える。
    *   `Start` メソッドの `b.Subscribe("system")` を
        `b.Subscribe(arcproto.TopicSystem)` に置き換える。
    *   置換完了後、`cpu.go` の import から `encoding/binary` が不要になるはずである。
    *   **`VRAMWidth` / `VRAMHeight` 定数（14-19 行）は本計画では削除しない。**
        シーン関数が使用しており、その解消は 028 の R7（能力照会）と 029 の責務である。

#### [MODIFY] [features/neurom/internal/modules/cpu/cpu_test.go](file://features/neurom/internal/modules/cpu/cpu_test.go)

*   **Description**: 既存の `TestCPUModule` は最初の 1 メッセージの `Source` が `"CPU"` かを
    確認するだけであり、`arcproto` 化後も無変更で PASS する想定。
    ただし最初のメッセージが `mode` であることの検証を追加する。
*   **Technical Design**:
    ```go
    select {
    case msg := <-b.ch:
        if msg.Source != "CPU" {
            t.Errorf("Expected source CPU, got %s", msg.Source)
        }
        // 追加: 最初のコマンドが mode であり、レガシー 3 バイトが維持されていること
        if msg.Target != arcproto.TargetMode {
            t.Errorf("Expected first target %s, got %s", arcproto.TargetMode, msg.Target)
        }
        if !bytes.Equal(msg.Data, []byte{0x00, 0x01, 0x00}) {
            t.Errorf("mode payload changed: got % X", msg.Data)
        }
    case <-time.After(time.Second):
        t.Fatal("Timeout waiting for CPU message")
    }
    ```
*   **Logic**:
    *   R6 の「バイト列不変」を CPU 側の実経路でも 1 点押さえる。

---

### 統合テスト（既存の変更）

#### [MODIFY] [features/neurom/integration/vram_page_test.go](file://features/neurom/integration/vram_page_test.go)

*   **Description**: R5。ローカルヘルパー `blitIntegration` と手書きの `read_rect` 組み立てを
    `arcproto` に置き換える。
*   **Technical Design**:
    ```go
    // Before（71-79 行）
    readData := make([]byte, 9)
    readData[0] = 0
    binary.BigEndian.PutUint16(readData[1:], 0)
    binary.BigEndian.PutUint16(readData[3:], 0)
    binary.BigEndian.PutUint16(readData[5:], 2)
    binary.BigEndian.PutUint16(readData[7:], 2)
    b.Publish("vram", &bus.BusMessage{
        Target: "read_rect", Operation: bus.OpCommand, Data: readData, Source: "test",
    })

    // After
    readCmd := arcproto.ReadRect{Page: 0, X: 0, Y: 0, W: 2, H: 2}
    b.Publish(arcproto.TopicVRAM, &bus.BusMessage{
        Target: readCmd.Target(), Operation: bus.OpCommand,
        Data: readCmd.Encode(), Source: "test",
    })
    ```
*   **Logic**:
    *   `blitIntegration(page, x, y, w, h, blend, pixels)` の定義本体を
        `arcproto.BlitRect{...}.Encode()` を使う実装に置き換える。
        **呼び出し側（複数箇所）は無変更で済む**ようシグネチャを維持する。
    *   `rect_data` レスポンスの解析（ピクセル値の取り出し）を
        `arcproto.DecodeRectData` に置き換える。

#### [MODIFY] [features/neurom/integration/vram_enhancement_test.go](file://features/neurom/integration/vram_enhancement_test.go)

*   **Description**: R5。`blitIntegration` の共有と、`set_palette_block` / `clear_vram` の
    手書き組み立てを `arcproto` に置き換える。
*   **Logic**:
    *   `blitIntegration` は `vram_page_test.go` と同一パッケージ内で共有されているため、
        定義は 1 箇所のみ置き換える。

#### [MODIFY] [features/neurom/integration/vram_multicore_test.go](file://features/neurom/integration/vram_multicore_test.go)

*   **Description**: R5。ローカルヘルパー `blitMultiCore` を `arcproto` に置き換える。
*   **Logic**:
    *   このファイルは `vram.BlendReplace`（`internal/modules/vram` の定数）を参照している。
        `blend.go` を型エイリアスにするため**この参照は無変更で動作する**。
    *   `blitMultiCore(page, x, y, w, h, blend vram.BlendMode, pixels)` の
        シグネチャを維持し、内部を `arcproto.BlitRect{...}.Encode()` に置き換える。
        `vram.BlendMode` は `arcproto.BlendMode` のエイリアスなので変換不要。

#### [MODIFY] [features/neurom/integration/palette_test.go](file://features/neurom/integration/palette_test.go), [stats_http_test.go](file://features/neurom/integration/stats_http_test.go), [vram_stats_test.go](file://features/neurom/integration/vram_stats_test.go), [vram_monitor_test.go](file://features/neurom/integration/vram_monitor_test.go)

*   **Description**: R5 / R2。`Publish("vram", ...)` と `Target: "set_palette"` 等の
    文字列リテラルを `arcproto` の定数・型に置き換える。
*   **Logic**:
    *   `vram_stats_test.go` 65-66 行の `get_stats` は
        `arcproto.GetStats{}` に置き換える（`Encode()` が `nil` を返す点に注意。
        現行も `Data` 未設定で `nil` のため一致する）。
    *   `stats_http_test.go` は 9 箇所で `Publish("vram", ...)` を行っている。
        すべて `arcproto.TopicVRAM` に置き換える。

---

## Step-by-Step Implementation Guide

### Phase 1: arcproto パッケージの構築（TDD）

1.  **テストを先に書く（Failed First）**:
    *   Create `features/neurom/arcproto/roundtrip_test.go` with round-trip tests for
        all 17 commands and 4 events, including boundary cases
        (zero, `u16` max 65535, `u8` max 255, 512×512 pixels, negative viewport offsets).
    *   Create `features/neurom/arcproto/golden_test.go` with fixed byte-sequence
        expectations derived by hand from the current `cpu.go` `publish*` offset arithmetic.
    *   Create `features/neurom/arcproto/types_test.go` with boundary tests for
        `Degrees`, `Turns`, `ScaleOf`.
    *   Run `./scripts/process/build.sh` and confirm it **fails to compile**
        (the types do not exist yet). This is the expected "red" state.

2.  **型と定数を実装する**:
    *   Create `features/neurom/arcproto/doc.go` with the package documentation
        (big-endian rule, no `internal/bus` dependency, Encode/Decode adjacency rule).
    *   Create `features/neurom/arcproto/types.go` with `ErrShortPayload`, `ErrBadPayload`,
        `Rotation` + `Degrees` + `Turns`, `Scale` + `ScaleOne` + `ScaleOf`,
        `BlendMode` + 5 constants (values 0x00-0x04 from `blend.go`), `Color`.
    *   Create `features/neurom/arcproto/topic.go` with the 6 topic constants.
    *   Create `features/neurom/arcproto/target.go` with 17 command targets,
        17 event targets, and the 2 page error codes (0x01, 0x03).
    *   Run `./scripts/process/build.sh` and confirm `types_test.go` now passes.

3.  **コマンド型を実装する**:
    *   Create `features/neurom/arcproto/vram_draw.go`
        (`Mode`, `DrawPixel`, `ClearVRAM`, `BlitRect`, `BlitRectTransform`, `CopyRect`).
    *   Create `features/neurom/arcproto/vram_palette.go`
        (`SetPalette`, `SetPaletteBlock`, `ReadPaletteBlock`).
    *   Create `features/neurom/arcproto/vram_page.go`
        (`SetPageCount` + `ResolvedCount`, `SetDisplayPage`, `SwapPages`, `CopyPage`, `SetPageSize`).
    *   Create `features/neurom/arcproto/vram_view.go` (`SetViewport`, signed int16).
    *   Create `features/neurom/arcproto/vram_read.go`
        (`ReadRect`, `GetStats`, `RectData`, `PaletteData` alias).
    *   Create `features/neurom/arcproto/event.go` (`RectUpdated`, `PageError`,
        plus the echo-event correspondence table in godoc).
    *   Run `./scripts/process/build.sh` and confirm **all** `arcproto` tests pass (green).

4.  **依存の隔離を確認する**:
    *   Verify `arcproto` does not import `internal/bus` (see Verification Plan step 1).

### Phase 2: VRAM 側のデコード置換

5.  **`blend.go` を型エイリアス化する**:
    *   Edit `features/neurom/internal/modules/vram/blend.go` to replace the
        `BlendMode` type definition and 5 constants with aliases to `arcproto`.
    *   Use `type BlendMode = arcproto.BlendMode` (**alias with `=`**, not a defined type).
    *   Keep `BlendPixel` and `clamp8` unchanged.
    *   Run `./scripts/process/build.sh` and confirm `blend_test.go` and
        `vram_multicore_test.go` still compile and pass.

6.  **`handle*` を 1 つずつ置換する**:
    *   Edit `features/neurom/internal/modules/vram/vram.go`.
    *   Replace handlers one at a time in this order (simplest first, to build confidence):
        `handleSetDisplayPage`, `handleSwapPages`, `handleCopyPage`, `handleSetPageCount`,
        `handleSetPageSize`, `handleSetViewport`, `handleDrawPixel`, `handleClearVRAM`,
        `handleSetPalette`, `handleReadPaletteBlock`, `handleSetPaletteBlock`,
        `handleReadRect`, `handleCopyRect`, `handleBlitRect`, `handleBlitRectTransform`,
        and the inline `case "mode"` body.
    *   For each handler: substitute the manual offset parsing with the `arcproto` Decode,
        keep the "silently return on error" behaviour, and leave all clipping,
        parallelisation (`v.parallelRows`) and pixel-writing logic untouched.
    *   In `handleSetPageCount`, use `c.ResolvedCount()` and delete the local
        `if count == 0 { count = 256 }`.
    *   Run `./scripts/process/build.sh` after **each** handler replacement.

7.  **Target とトピックを定数化する**:
    *   Edit `features/neurom/internal/modules/vram/vram.go` to replace the `switch msg.Target`
        case literals with `arcproto.Target*` constants.
    *   Replace all `v.bus.Publish("vram_update", ...)` with `arcproto.TopicVRAMUpdate`
        and all event `Target:` literals with `arcproto.Event*` constants.
    *   Replace `v.publishPageError(0x01)` / `(0x03)` with
        `arcproto.PageErrInvalidPage` / `arcproto.PageErrInvalidDisplay`.
    *   Replace `b.Subscribe("vram")` / `b.Subscribe("system")` with
        `arcproto.TopicVRAM` / `arcproto.TopicSystem`.
    *   Confirm `encoding/binary` is no longer needed in the import block.
        If it is still needed, a replacement was missed.
    *   Run `./scripts/process/build.sh`.

### Phase 3: CPU 側のエンコード置換

8.  **`publishCmd` ヘルパーを導入する**:
    *   Edit `features/neurom/internal/modules/cpu/cpu.go` to add the `command` interface
        (`Target() string`, `Encode() []byte`) and the `publishCmd` method.

9.  **`publish*` 12 関数の内側を置換する**:
    *   Edit `features/neurom/internal/modules/cpu/cpu.go`.
    *   **Do not change any `publish*` signature** and **do not touch scene functions**
        (lines 247-520).
    *   Replace bodies of: `publishClearVRAM`, `publishBlitRect`, `publishBlitRectTransform`,
        `publishCopyRect`, `publishSetPaletteBlock`, `publishSetPageCount`,
        `publishSetDisplayPage`, `publishSwapPages`, `publishCopyPage`,
        `publishSetPageSize`, `publishSetViewport`.
    *   In `publishSetPaletteBlock`, convert `[][4]uint8` to `[]arcproto.Color`
        honouring the `count` argument (pass `colors[:count]`).
    *   Replace the inline `mode` publish in `run` (lines 63-67) with
        `c.publishCmd(b, arcproto.Mode{})`.
    *   Replace `b.Subscribe("system")` with `arcproto.TopicSystem`.
    *   Confirm `encoding/binary` is no longer needed in the import block.
    *   Run `./scripts/process/build.sh`.

10. **`cpu_test.go` に mode バイト列の検証を追加する**:
    *   Edit `features/neurom/internal/modules/cpu/cpu_test.go` to assert
        `msg.Target == arcproto.TargetMode` and `msg.Data == []byte{0x00, 0x01, 0x00}`.
    *   Run `./scripts/process/build.sh`.

### Phase 4: 統合テストの共通化

11. **エンコードヘルパーを `arcproto` に寄せる**:
    *   Edit `features/neurom/integration/vram_page_test.go` to reimplement
        `blitIntegration` on top of `arcproto.BlitRect` (keeping its signature),
        and replace the hand-built `read_rect` payload.
    *   Edit `features/neurom/integration/vram_multicore_test.go` to reimplement
        `blitMultiCore` on top of `arcproto.BlitRect` (keeping its signature).
    *   Edit `features/neurom/integration/vram_enhancement_test.go`,
        `palette_test.go`, `stats_http_test.go`, `vram_stats_test.go`,
        `vram_monitor_test.go` to use `arcproto` constants and types
        instead of string literals and hand-built payloads.
    *   Replace `rect_data` response parsing with `arcproto.DecodeRectData`.
    *   Run `./scripts/process/build.sh`.

### Phase 5: 仕様書の作成

12. **プロトコル仕様書を書き起こす**:
    *   Edit `prompts/specifications/VRAM-Specification.md` per the Documentation section below.
    *   Cross-check the command table 1:1 against the `switch msg.Target` branches
        in `features/neurom/internal/modules/vram/vram.go`.

### Phase 6: 検証

13. **Verification Plan を実行する**:
    *   Execute every step in the Verification Plan section below, in order.
    *   Record the comprehensive verdict (§12 of the testing rules) at the end.

## Verification Plan

### テスト項目設計 (Test Item Design)

テストルール §11 に従い、ボトムアップ順序でテスト項目を設計した。

#### ボトムアップの確認順序

依存関係は `統合テスト/CPU/VRAM → arcproto` である（`arcproto` が末端）。

```
依存関係:  cpu.publish* ─┐
           vram.handle* ─┼→ arcproto
           integration  ─┘

テスト順序:
  Step 1: arcproto の単体テスト        → プロトコル層が実際に動作していることを確認
  Step 2: vram / cpu の単体テスト       → arcproto を前提に各モジュールの振る舞いを確認
  Step 3: integration の統合テスト      → 全体を通したバイト列と描画結果の同一性を確認
  Step 4: 実バイナリの目視確認           → 人間が見て意図どおりであることを確認
```

#### 観点チェックリスト（§11.3）

| # | 観点 | 本計画での対応 |
|---|------|----------------|
| 1 | 正常系の動作確認 | `golden_test.go` が全 17 コマンドの既知入力→既知バイト列を検証。`roundtrip_test.go` が典型値を検証 |
| 2 | 異常系・境界値 | `roundtrip_test.go` が長さ不足で `ErrShortPayload` を検証。境界値は 0 / `u8` 最大 255 / `u16` 最大 65535 / `int16` 最小 -32768 / `count = 0` / 512×512 |
| 3 | 外部連携の実動作 | バス（`ChannelBus`）を介した VRAM への到達を既存統合テストで確認。`/stats` HTTP エンドポイントの応答も確認 |
| 4 | データの一貫性 | `roundtrip_test.go` が Encode→Decode の可逆性を検証。統合テストが `blit_rect` 書き込み→`read_rect` 読み出しの一致を検証 |
| 5 | 状態遷移の検証 | 既存の `TestPageManagementIntegration` がページ数変更前後の状態を検証。`TestPageDrawIsolationIntegration` がページ間の独立性を検証 |
| 6 | 設定・構成の反映 | `BlendMode` の 5 モードすべてが正しい値でエンコードされ、既存の `TestBlendModes`（`vram_enhancement_test.go`）で描画結果として反映されることを確認 |
| 7 | 副作用の確認 | `arcproto` が `internal/bus` に依存していないことを `go list -deps` で確認（意図しない結合の排除）。`Decode` がサブスライスを返し余分な確保をしないことを確認 |

#### セルフレビュー（§11.4）

1.  **網羅性の検証**: 「このテスト項目群が全て成功した場合、この機能が実際に動作していると言えるか？」
    →  **言える。** 本計画の成否は「バイト列が 1 ビットも変わらないか」に集約される。
    `golden_test.go` が全 17 コマンドの生成バイト列を固定値で押さえ、
    さらに既存の VRAM 統合テスト（描画結果の実測値検証）が無変更で PASS することで、
    エンコード側とデコード側の両方の同一性が二重に保証される。
    加えて `cpu_test.go` が実際のバス経路上のバイト列を 1 点押さえる。

2.  **証拠の十分性**: 「各テスト項目は、動作していることの証拠を得られるレベルに充実しているか？」
    →  **充実している。** 「エラーが出ない」ではなく、
    `golden_test.go` は期待バイト列との完全一致、
    統合テストは `read_rect` で読み戻したピクセル値の一致、
    `types_test.go` は変換結果の数値一致を確認している。

3.  **迂回・抜け道の排除**: 「テストが成功しても、実は別の経路で処理されている可能性はないか？」
    →  **排除できている。** 懸念は「`vram.go` が `arcproto` を使わず古い手書き解析のまま
    残っている箇所があり、それでもテストが通ってしまう」ケース。
    これは `vram.go` / `cpu.go` の import から `encoding/binary` が消えることを
    確認することで検出する（Verification Plan 自動検証 6）。
    手書きオフセット計算が 1 つでも残れば `encoding/binary` が必要なままになる。

4.  **依存関係の整合性**: 「呼び出し先のテストが成功していなければ、呼び出し元のテストの成功に意味がない」
    →  **崩れていない。** Step 1（`arcproto` 単体）を先に緑にしてから
    Step 2（vram / cpu）、Step 3（統合）へ進む手順を Step-by-Step Implementation Guide で強制している。

### Automated Verification

> テストルール §2 に従い、統合テストの前に必ずビルドを成功させる。
> 計画立案規範 §3.1 に従い、生の `go build` / `go test` は使用しない。

1.  **依存の隔離確認（R1）**:
    `arcproto` が `internal/bus` および `internal/modules` に依存していないことを確認する。
    ```bash
    ./scripts/process/build.sh
    ```
    *   **確認内容**: ビルドが成功すること。加えて、`features/neurom/arcproto/` 配下の
        全ファイルの import 文をレビューし、`github.com/axsh/neurom/internal/` で始まる
        import が **0 件**であることを確認する。
    *   **根拠**: `arcproto` が `internal/` に依存すると、
        029 で `features/metov` から使う際に依存が漏れる。

2.  **Build & Unit Tests（R3, R4, R6, R8, R9）**:
    ビルドスクリプトを実行する。`arcproto` の単体テスト、`vram` / `cpu` の単体テスト、
    および `features/neurom/integration/` の統合テストがすべて実行される。
    ```bash
    ./scripts/process/build.sh
    ```
    *   **確認内容**:
        *   `TestGoldenEncode` の全ケースが PASS すること（**R6 の中核証拠**）。
        *   `TestRoundTrip*` の全ケースが PASS すること。
        *   `TestDegrees` / `TestTurns` / `TestScaleOf` の全境界値が PASS すること。
        *   `TestCPUModule` が PASS し、`mode` の 3 バイトが維持されていること。
        *   既存の `vram` パッケージ単体テスト（`vram_test.go`, `blend_test.go`,
            `transform_test.go`, `worker_test.go`, `dispatcher_test.go`）が
            **無変更で** PASS すること。
        *   `bin/neurom.exe` と `bin/stats.exe` が生成されること。
    *   **Log Verification**: 出力に `[FAIL]` が無いこと。
        `--- FAIL` や `panic` の文字列が無いこと。
        `[PASS] Build pipeline PASSED` で終了すること。

3.  **Integration Tests（R6）**:
    ビルド成功後に統合テストを実行する。
    ```bash
    ./scripts/process/build.sh && ./scripts/process/integration_test.sh --specify "TestPageManagementIntegration|TestPageDrawIsolationIntegration|TestPaletteUpdate|TestVRAMMonitorIntegration|TestBlendModes|TestClearVRAM"
    ```
    *   **Log Verification**: `[PASS] All integration tests passed!` が出力されること。
    *   **重要な現状の注意**: `integration_test.sh` はリポジトリルートの `tests/go.mod` の
        存在を前提とするが、現状 `tests/` ディレクトリは存在しないため
        `[WARN] tests/ directory not found` を出して exit 0 する（実質 no-op）。
        したがって**本計画における統合テストの実効的な検証は上記手順 2 の `build.sh` が担う**
        （`build.sh` は `go list ./... | grep -v '/tests/'` で列挙するため、
        パス名が `/tests/` に一致しない `features/neurom/integration/` を対象に含む）。
        統合テストを `tests/` へ移設して `integration_test.sh` を実効化する作業は
        本計画のスコープ外とし、別途課題として記録する。

4.  **リグレッション反復確認（R6）**:
    並列実行やタイミングに依存した不安定さが混入していないことを確認する。
    ```bash
    ./scripts/process/build.sh
    ```
    *   **確認内容**: 上記を **3 回連続**実行し、3 回とも PASS すること。
    *   **根拠**: `vram` は `--cpu N` のワーカープールと `adaptive` ディスパッチャを持ち、
        `dispatcher_test.go` / `parallel_bench_test.go` がタイミングに依存する。
        `arcproto` 化は並列部分に触れないが、`Decode` がサブスライスを返す変更により
        データ競合が生じていないことを反復実行で確認する。

5.  **ゴールデンバイト列の網羅性確認（R8）**:
    `golden_test.go` のケース数が 17 コマンドすべてを覆っていることを確認する。
    *   **確認内容**: `features/neurom/arcproto/golden_test.go` のテストケース名一覧と、
        `features/neurom/arcproto/target.go` のコマンド定数 17 個を突き合わせ、
        欠落が 0 件であることをレビューで確認する。

6.  **置換漏れの検出（R5）**:
    手書きオフセット計算が残っていないことを確認する。
    *   **確認内容**:
        *   `features/neurom/internal/modules/vram/vram.go` の import に
            `encoding/binary` が**含まれないこと**。
        *   `features/neurom/internal/modules/cpu/cpu.go` の import に
            `encoding/binary` が**含まれないこと**。
        *   `features/neurom/internal/modules/` および `features/neurom/integration/` 配下に
            `binary.BigEndian.PutUint16` の呼び出しが**残っていないこと**。
        *   `features/neurom/internal/modules/` および `features/neurom/integration/` 配下に
            `"blit_rect"` / `"clear_vram"` / `"vram_update"` 等の
            コマンド・イベント・トピックの文字列リテラルが**残っていないこと**
            （`arcproto` 内の定数定義箇所を除く）。
    *   **根拠**: セルフレビュー 3（迂回・抜け道の排除）で特定した検出手段。

### E2E Tests

**E2E テストは追加しない。** 理由を以下に明記する。

*   本計画は**振る舞いを一切変更しない純粋な内部リファクタリング**であり、
    新しいユーザー可視の機能は追加されない
    （計画立案規範 §2.1 および create-implementation-plan §3 の
    「E2E テストが不要な場合（純粋な内部リファクタリング等）」に該当する）。
*   検証すべき唯一の性質は「生成バイト列と描画結果が変化しないこと」であり、
    これは `golden_test.go`（バイト列の固定値検証）と
    既存の `features/neurom/integration/` 配下の統合テスト
    （バス経由の描画結果の実測値検証）で完全に覆われる。
*   加えて、リポジトリルートに `tests/` ディレクトリと `tests/go.mod` が存在しないため、
    `integration_test.sh` が要求する E2E テストの配置場所自体が未整備である。
    この整備は本計画のスコープ外とし、別途課題として記録する。

### 目視確認（補助的）

計画立案規範 §3.3 に従い、以下は自動検証の**代替ではなく補助**として実施する。
機能ロジックの検証は上記の自動検証が担う。

1.  **デモ表示の同一性（R6）**:
    `./bin/neurom.exe` を 21 秒以上起動し、7 シーンすべてを通す。
    *   **確認内容**: 各シーンの見た目が本計画の変更前と同一であること。
    *   **注意**: シーン 2（BlitRect）は現状すでにコマンド破棄により部分描画になっている。
        本計画ではこの挙動も**変わらない**ことが正しい
        （破棄の解消は 025 / 027 の担当）。

2.  **統計名の同一性（R6）**:
    `./bin/neurom.exe --headless --stats-port 8080` を起動し、
    `bin/stats.exe --endpoint http://localhost:8080/stats` で統計を取得する。
    *   **確認内容**: 表示されるコマンド名の集合が変更前と一致すること。
        特に `rect_updated` / `vram_cleared` 等のイベント名が混入している現状の挙動も
        **変わらない**ことが正しい（解消は 025 の担当）。

### 総合判定プロセス (Post-Test Comprehensive Verdict)

テストルール §12 に従い、全テスト完了後に以下を実施し、結果を記録する。
**「全テスト成功 → 動作確認完了」と機械的に判定してはならない。**

1.  §12.2 のチェック項目 1〜7 を一つずつ確認する。本計画で特に注意すべき点:
    *   **項目 1（スキップ）**: `build.sh` の出力に `[WARN] No Go unit test packages found`
        が出ていないこと。`arcproto` パッケージのテストが実際に実行されていること。
    *   **項目 2（部分的なエラー）**: 統合テストのログに
        `[VRAM] run: ctx.Done` 以外の `ERROR` / `panic` / `recovered` が無いこと。
    *   **項目 3（迂回処理による偽成功）**: 自動検証 6（置換漏れの検出）の結果。
        古い手書き解析が残ったまま通っていないこと。
    *   **項目 6（カバレッジの妥当性）**: 自動検証 5（ゴールデンの網羅性）の結果。
        17 コマンドすべてにゴールデンケースがあること。
2.  §12.3 のフォーマットで総合判定を記述する。
3.  判定結果を本計画書の末尾、またはウォークスルーに追記する。

## Documentation

`prompts/specifications/` 配下の既存ドキュメントを解析し、本計画で影響を受けるものを更新する。

#### [MODIFY] [prompts/specifications/VRAM-Specification.md](file://prompts/specifications/VRAM-Specification.md)

*   **更新内容**: 現状 3 行の `**To Be Described**` プレースホルダを、
    実装ベースのプロトコル仕様書として書き起こす（R7）。以下の節を含める。

    1.  **概要と契約の宣言**
        この文書がプロトコルの契約書であり、実装が唯一の真実である状態を解消することを明記する。
    2.  **共通規定**
        *   全整数はビッグエンディアン。
        *   座標・サイズは `u16`、ビューポートオフセットのみ `i16`（2 の補数）。
        *   回転は 0-255 で一周（`Rotation`）。
        *   拡大率は 8.8 固定小数点、`0x0100` が等倍（`Scale`）。0 以下は等倍に丸められる。
        *   ページ番号は `u8`、パレットインデックスは `u8`（256 段）。
        *   パレット色は RGBA 各 8bit。
    3.  **トピック一覧**
        `vram`（コマンド）、`vram_update`（イベント）、`monitor` / `monitor_update`、`system`、`io`。
        **現状の購読は前方一致であり、`vram` の購読者が `vram_update` も受信してしまう**
        という既知の欠陥を注記し、025 で是正予定であることを記載する。
    4.  **コマンド一覧（全 17 件）**
        Target・payload バイトレイアウト・境界条件・不正時の挙動・発行されるイベントを
        表形式で記載する。`vram.go` の `handleMessage` の switch 分岐と 1:1 対応させる。
    5.  **イベント一覧（全 17 件）**
        Target・payload レイアウト・発行契機。
        コマンド payload のエコーであるものはその対応を明記する。
    6.  **エラーコード**
        `0x01` = 無効なページ番号、`0x03` = 無効な表示ページ指定。
        **`0x02` は現行実装に存在しない欠番**であることを明記する。
    7.  **座標系とクリッピング規則**
        `vram.go` の `clipRect` の挙動（負座標は転送元オフセットで吸収、
        右下は幅高で切り詰め、結果が空なら描画しない）を記述する。
    8.  **既知の制約**
        *   `read_rect` の応答 `rect_data` に**読み出し元ページが含まれない**ため、
            要求元が自分で覚えておく必要がある。
        *   `read_rect` / `read_palette_block` に**相関 ID が無い**ため、
            並行読み出しを対応付けられない（025 で解決予定）。
        *   `mode` コマンドの payload は現行実装では**参照されない**。
            `cpu.go` が送る 3 バイト `00 01 00` は互換のため維持されている。
        *   `set_page_size` に**上限検証が無い**（028 で解決予定）。
        *   バスは購読チャネル満杯時にメッセージを**黙って破棄する**（025 で可視化予定）。
    9.  **既存仕様書との齟齬（R10）**
        `ideas/012-VRAMPage.md` 83-84 行の `blit_rect` / `blit_rect_transform` は
        `[src_page:u8][dst_page:u8]` の 2 ページ指定として記述されているが、
        **実装は `[page:u8]` の 1 個のみ**である。実装を正とする。

#### [MODIFY] [prompts/specifications/ARC-Architecture.md](file://prompts/specifications/ARC-Architecture.md)

*   **更新内容**: 21-27 行「通信バス (ZeroMQ)」節に、プロトコル仕様書への参照を追記する。
    多言語対応（26 行）については、**現状のエンベロープが Go 専用の `encoding/gob` であり
    未達成である**ことを注記し、028 で解決予定であることを記載する。
    アーキテクチャの構成要素自体（CPU モジュールの位置づけ等）の変更は
    029 の担当であり、本計画では行わない。

## 継続計画について

本計画は単一 Part で完結する。分割は行わない。

後続の実装計画は、それぞれの仕様書が承認された後に個別に作成する。

| 仕様 | 内容 | 本計画との関係 |
|---|---|---|
| 025 | バッチコマンド / 相関 ID / トピック整理 | `arcproto` に `CommandID` と `Batch` を追加する |
| 026 | 入力とフレームクロック | `arcproto` に `Button` / `KeyRaw` / `Vsync` / `InputState` を追加する |
| 027 | `arc` SDK と in-process 実行 | `cpu.go` の `publish*` を廃止し、シーンを SDK ベースへ書き換える |
| 028 | 外部バスの双方向化 | エンベロープをバイナリ化し、仕様書に追記する |
| 029 | Metov の別プロセス分離 | `arcproto` / `arc` を `features/metov` から import する |
