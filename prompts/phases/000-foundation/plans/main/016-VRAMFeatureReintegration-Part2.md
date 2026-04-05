# 016-VRAMFeatureReintegration-Part2

> **Source Specification**: `prompts/phases/000-foundation/ideas/main/016-VRAMFeatureReintegration.md`

## Goal Description

VRAMページ機能（`pageBuffer` 構造体、ページ管理コマンド4種、全描画コマンドのページ引数化）、ページ単位サイズ管理、ビューポートオフセット、および Monitor モジュールの `VRAMAccessor` 直接参照パイプラインを実装する。Part 1 で構築した二重バッファ構造をページ単位に拡張する。

## User Review Required

- `blit_rect` / `blit_rect_transform` の `src_page` パラメータは 013-VRAMViewport R10 に基づき廃止する。ブレンド背景は `dst_page` から読み取る。
- ビューポートオフセットの型は `int16`（負値許容）。

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
|:---|:---|
| R3: pageBuffer 構造体 | Proposed Changes > vram.go — pageBuffer struct |
| R3: set_page_count | Proposed Changes > vram.go — handleSetPageCount |
| R3: set_display_page | Proposed Changes > vram.go — handleSetDisplayPage |
| R3: swap_pages | Proposed Changes > vram.go — handleSwapPages |
| R3: copy_page | Proposed Changes > vram.go — handleCopyPage |
| R3: 全コマンドのページ引数化 | Proposed Changes > vram.go — 全ハンドラ修正 |
| R4: pageBuffer に width/height | Proposed Changes > vram.go — pageBuffer 拡張 |
| R4: set_page_size | Proposed Changes > vram.go — handleSetPageSize |
| R4: ViewportOffset() | Proposed Changes > vram.go — set_viewport + ViewportOffset |
| R4: blit 系 src_page 廃止 | Proposed Changes > vram.go — handleBlitRect/Transform 修正 |
| R5: VRAMAccessor インターフェース | Proposed Changes > monitor.go — VRAMAccessor interface |
| R5: 拡張イベントハンドラ | Proposed Changes > monitor.go — receiveLoop 拡張 |
| R5: displayPage フィルタリング | Proposed Changes > monitor.go — receiveLoop 内 |
| R5: dirty フラグ＋直接リフレッシュ | Proposed Changes > monitor.go — directRefreshLoop |

## Proposed Changes

### vram モジュール

#### [MODIFY] [features/neurom/internal/modules/vram/vram_test.go](file://features/neurom/internal/modules/vram/vram_test.go)

* **Description**: ページ管理とビューポートの単体テストを追加する
* **Technical Design**:

  ```go
  func TestPageManagement(t *testing.T)
  // - set_page_count(2): pages スライスが len=2 になること
  // - set_page_count(1): 末尾ページが破棄されること
  // - displayPage がページ数以上のとき 0 にリセットされること
  // - "page_count_changed" イベント検証

  func TestSetDisplayPage(t *testing.T)
  // - set_display_page(1): displayPage が 1 になること
  // - 存在しないページ番号 → "page_error" イベント発行、displayPage 不変
  // - "display_page_changed" イベント検証

  func TestSwapPages(t *testing.T)
  // - page 0 にパターンA、page 1 にパターンB を書き込み
  // - swap_pages(0,1) 実行
  // - page 0 がパターンB、page 1 がパターンA であること
  // - "pages_swapped" イベント検証

  func TestCopyPage(t *testing.T)
  // - page 0 にパターン書き込み、page 1 は空
  // - copy_page(0,1) 実行
  // - page 1 が page 0 と同一内容であること
  // - src と dst が同一の場合は何もしないこと
  // - "page_copied" イベント検証

  func TestDrawPixelWithPage(t *testing.T)
  // - draw_pixel の新フォーマット [page:u8][x:u16][y:u16][p:u8]
  // - 指定ページにのみ書き込まれること

  func TestClearVRAMWithPage(t *testing.T)
  // - clear_vram の新フォーマット [page:u8][palette_idx:u8]
  // - 指定ページのみクリアされること、他ページは不変

  func TestBlitRectWithPage(t *testing.T)
  // - blit_rect の新フォーマット [dst_page:u8][dst_x:u16]...(src_page 廃止)
  // - 指定ページに書き込まれること

  func TestCopyRectCrossPage(t *testing.T)
  // - copy_rect [src_page:u8][dst_page:u8][src_x:u16]...
  // - ページ間コピーが正しく動作すること

  func TestPageSizeManagement(t *testing.T)
  // - set_page_size で page 0 を 512x512 にリサイズ
  // - バッファサイズが 512*512 になること
  // - "page_size_changed" イベント検証
  // - 他ページのサイズは不変

  func TestCopyPageResizes(t *testing.T)
  // - page 0 = 512x512, page 1 = 256x212
  // - copy_page(0, 1) → page 1 が 512x512 にリサイズされること

  func TestCopyRectCrossPageClipping(t *testing.T)
  // - page 0 = 512x512, page 1 = 256x212
  // - copy_rect で page 0 の (400,400,100,100) → page 1 の (200,200)
  // - page 1 側でクリッピングされること

  func TestViewportOffset(t *testing.T)
  // - set_viewport (100, 50) でオフセット設定
  // - ViewportOffset() が (100, 50) を返すこと
  // - "viewport_changed" イベント検証

  func TestInvalidPageError(t *testing.T)
  // - 存在しないページへの各操作で "page_error" イベントが発行されること
  ```

#### [MODIFY] [features/neurom/internal/modules/vram/vram.go](file://features/neurom/internal/modules/vram/vram.go)

* **Description**: `pageBuffer` 構造体の導入、ページ管理コマンド、全コマンドのページ引数化、ビューポート
* **Technical Design**:

  **新しいデータ構造:**
  ```go
  const (
      DefaultPageWidth  = 256
      DefaultPageHeight = 212
  )

  type pageBuffer struct {
      width  int
      height int
      index  []uint8 // palette index buffer (width * height)
      color  []uint8 // RGBA color buffer (width * height * 4)
  }

  func newPageBuffer(w, h int) pageBuffer {
      return pageBuffer{
          width: w, height: h,
          index: make([]uint8, w*h),
          color: make([]uint8, w*h*4),
      }
  }

  type VRAMModule struct {
      mu          sync.RWMutex
      pages       []pageBuffer    // pages[0] が初期ページ
      displayPage int
      viewportX   int16           // 表示オフセット X (int16, 負値可)
      viewportY   int16           // 表示オフセット Y (int16, 負値可)
      palette     [256][4]uint8
      bus         bus.Bus
      wg          sync.WaitGroup
      stats       *stats.Collector
  }
  ```

  **New() の変更:**
  ```go
  func New() *VRAMModule {
      return &VRAMModule{
          pages:   []pageBuffer{newPageBuffer(DefaultPageWidth, DefaultPageHeight)},
          stats:   stats.NewCollector(),
      }
  }
  ```

  **ページ管理コマンドハンドラ:**
  ```go
  func (v *VRAMModule) handleSetPageCount(msg *bus.BusMessage)
  // data: [count:u8] — 0=256ページ, 1-255=そのまま
  // 増加: 末尾に newPageBuffer(DefaultPageWidth, DefaultPageHeight) を追加
  // 減少: 末尾から破棄。displayPage >= count なら displayPage = 0
  // イベント: "page_count_changed" [count:u8]

  func (v *VRAMModule) handleSetDisplayPage(msg *bus.BusMessage)
  // data: [page:u8]
  // 存在しないページ → publishPageError(0x03)
  // イベント: "display_page_changed" [page:u8]

  func (v *VRAMModule) handleSwapPages(msg *bus.BusMessage)
  // data: [page1:u8][page2:u8]
  // pages[page1], pages[page2] = pages[page2], pages[page1]
  // イベント: "pages_swapped" [page1:u8][page2:u8]

  func (v *VRAMModule) handleCopyPage(msg *bus.BusMessage)
  // data: [src:u8][dst:u8]
  // src == dst → 何もしない (冪等)
  // dst のサイズを src に合わせてリサイズ、内容コピー
  // イベント: "page_copied" [src:u8][dst:u8]

  func (v *VRAMModule) handleSetPageSize(msg *bus.BusMessage)
  // data: [page:u8][w_hi:u8][w_lo:u8][h_hi:u8][h_lo:u8]
  // バッファを新サイズで再確保、内容クリア
  // イベント: "page_size_changed" [page:u8][w_hi:u8][w_lo:u8][h_hi:u8][h_lo:u8]

  func (v *VRAMModule) handleSetViewport(msg *bus.BusMessage)
  // data: [offX_hi:u8][offX_lo:u8][offY_hi:u8][offY_lo:u8] (int16 BE)
  // イベント: "viewport_changed" [offX_hi:u8][offX_lo:u8][offY_hi:u8][offY_lo:u8]

  func (v *VRAMModule) publishPageError(code uint8)
  // "page_error" イベント発行: [error_code:u8]

  func (v *VRAMModule) isValidPage(p int) bool
  // 0 <= p < len(v.pages)
  ```

  **全描画コマンドの修正（ページ引数追加）:**

  `draw_pixel`: `[page:u8][x:u16][y:u16][p:u8]` (6 bytes)
  - `pages[page]` の index/color に書き込む

  `clear_vram`: `[page:u8][palette_idx:u8]` (2 bytes, palette_idx省略可)
  - `pages[page]` の index/color を塗りつぶす

  `blit_rect`: `[dst_page:u8][dst_x:u16][dst_y:u16][w:u16][h:u16][blend_mode:u8][pixel_data...]` (10+ bytes)
  - **`src_page` は廃止** (013 R10)。ブレンド背景は `dst_page` から読む
  - `pages[dst_page]` の width/height でクリッピング

  `blit_rect_transform`: `[dst_page:u8][dst_x:u16][dst_y:u16][src_w:u16][src_h:u16][pivot_x:u16][pivot_y:u16][rotation:u8][scale_x:u16][scale_y:u16][blend_mode:u8][pixel_data...]` (19+ bytes)
  - `src_page` 廃止。ブレンド背景は `dst_page` から

  `copy_rect`: `[src_page:u8][dst_page:u8][src_x:u16][src_y:u16][dst_x:u16][dst_y:u16][w:u16][h:u16]` (14 bytes)
  - ソースは `pages[src_page]`、デスティネーションは `pages[dst_page]`
  - 各ページの width/height でそれぞれクリッピング
  - 同一ページ内コピーではオーバーラップ対策を維持

  `read_rect`: `[page:u8][x:u16][y:u16][w:u16][h:u16]` (9 bytes)
  - `pages[page]` から読み出し

  **VRAMAccessor 用メソッドの更新:**
  ```go
  func (v *VRAMModule) VRAMBuffer() []uint8       // pages[displayPage].index
  func (v *VRAMModule) VRAMColorBuffer() []uint8   // pages[displayPage].color
  func (v *VRAMModule) VRAMWidth() int             // pages[displayPage].width
  func (v *VRAMModule) VRAMHeight() int            // pages[displayPage].height
  func (v *VRAMModule) VRAMPalette() [256][4]uint8
  func (v *VRAMModule) DisplayPage() int
  func (v *VRAMModule) ViewportOffset() (int16, int16)
  ```

### monitor モジュール

#### [MODIFY] [features/neurom/internal/modules/monitor/monitor_test.go](file://features/neurom/internal/modules/monitor/monitor_test.go)

* **Description**: VRAMAccessor と拡張イベントのテストを追加
* **Technical Design**:
  ```go
  // mockVRAMAccessor — VRAMAccessor インターフェースのモック
  type mockVRAMAccessor struct {
      index   []uint8
      color   []uint8
      width   int
      height  int
      palette [256][4]uint8
  }

  func TestMonitorDirectRefresh(t *testing.T)
  // - mockVRAMAccessor に index/color データを設定
  // - MonitorModule に SetVRAMAccessor で設定
  // - 一定時間後に GetPixel が VRAMAccessor のデータを反映していること

  func TestMonitorPageFilter(t *testing.T)
  // - displayPage=0 の状態でページ1の vram_updated を送信
  // - Monitor の内部バッファが変更されないこと
  ```

#### [MODIFY] [features/neurom/internal/modules/monitor/monitor.go](file://features/neurom/internal/modules/monitor/monitor.go)

* **Description**: `VRAMAccessor` インターフェースの型付け、直接リフレッシュ、拡張イベントハンドラ
* **Technical Design**:

  **VRAMAccessor インターフェース:**
  ```go
  type VRAMAccessor interface {
      VRAMBuffer() []uint8
      VRAMColorBuffer() []uint8
      VRAMWidth() int
      VRAMHeight() int
      VRAMPalette() [256][4]uint8
      DisplayPage() int
      ViewportOffset() (int16, int16)
  }
  ```

  **MonitorModule の変更:**
  ```go
  type MonitorModule struct {
      // ... 既存フィールド ...
      vramAccessor VRAMAccessor  // any → VRAMAccessor に型変更
      displayPage  int           // Bus モード用の表示ページ追跡
      palette      [256][4]uint8 // [3] → [4] に拡張
  }

  func (m *MonitorModule) SetVRAMAccessor(a VRAMAccessor) // 型変更
  ```

  **直接リフレッシュ（VRAMAccessor 使用時）:**
  ```go
  func (m *MonitorModule) refreshFromVRAM()
  // - v := m.vramAccessor
  // - index := v.VRAMBuffer(), color := v.VRAMColorBuffer()
  // - pal := v.VRAMPalette()
  // - vpX, vpY := v.ViewportOffset()
  // - vw, vh := v.VRAMWidth(), v.VRAMHeight()
  // - 表示領域 (vpX, vpY, m.width, m.height) を VRAMからコピー
  // - indexBuffer[i] == DirectColorMarker (0xFF) の場合は colorBuffer から RGBA
  // - それ以外は pal[indexBuffer[i]] から RGBA
  // - ビューポート範囲外はパレット 0 の色で埋める
  ```

  **receiveLoop の拡張:**
  - `vram_updated`: データ先頭に `[page:u8]` が追加される。`displayPage` と一致しないページのイベントは無視
  - `vram_cleared`: `[page:u8][palette_idx:u8]` — ページフィルタリング後、内部 vram バッファをクリア
  - `rect_updated`: ページフィルタリング。full/compact/header-only の 3 フォーマット対応
  - `rect_copied`: ページフィルタリング。header-only → dirty 設定
  - `display_page_changed`: `[page:u8]` — `m.displayPage` を更新、dirty 設定
  - `page_size_changed`: dirty 設定
  - `viewport_changed`: dirty 設定

  **RunMain の OnClose 統合:**
  - `StageDead` 検出時に `m.config.OnClose` が nil でなければ呼び出す（`publishShutdown()` に加えて）
  - これにより Part 3 で `cancel` を `OnClose` に設定した場合に、ウィンドウ閉じ → context キャンセル → 全モジュール停止の流れが成立する

  **buildFrame の修正:**
  - `palette[p]` を `[4]uint8` に対応（A チャネルを使用）
  - `indexBuffer[i] == 0xFF` のとき、colorBuffer から RGBA を直接コピー

### 統合テスト

#### [NEW] [features/neurom/integration/vram_page_test.go](file://features/neurom/integration/vram_page_test.go)

* **Description**: Bus 経由のページ管理の統合テスト
* **Technical Design**:
  ```go
  func TestPageManagementIntegration(t *testing.T)
  // - set_page_count(2) を送信、"page_count_changed" を確認

  func TestPageDrawIsolationIntegration(t *testing.T)
  // - page 0 にパターン描画、page 1 は空
  // - read_rect で各ページのデータを確認

  func TestPageDisplayIntegration(t *testing.T)
  // - page 0 にパターンA、page 1 にパターンB
  // - set_display_page(1) → Monitor.GetPixel がパターンB を返すこと

  func TestPageSizeIntegration(t *testing.T)
  // - set_page_size(0, 512, 512)
  // - blit_rect で (400, 400) に書き込み成功
  ```

## Step-by-Step Implementation Guide

- [x] 1. **vram_test.go にページテスト追加**: `TestPageManagement`, `TestSetDisplayPage`, `TestSwapPages`, `TestCopyPage`
- [x] 2. **pageBuffer 構造体の導入**: `newPageBuffer`、`VRAMModule` を `pages []pageBuffer` に移行、`New()` 更新
- [x] 3. **Part 1 の indexBuffer/colorBuffer 参照を pages[0] 経由に変更**: 全既存ハンドラの修正
- [x] 4. **ビルド確認**: `./scripts/process/build.sh` — 既存テストが通ること
- [x] 5. **ページ管理コマンド実装**: `handleSetPageCount`, `handleSetDisplayPage`, `handleSwapPages`, `handleCopyPage`, `publishPageError`, `isValidPage`
- [x] 6. **ページテスト確認**: 全ページ管理テストがパスすること
- [x] 7. **vram_test.go にページ引数テスト追加**: `TestDrawPixelWithPage`, `TestClearVRAMWithPage`, `TestBlitRectWithPage`, `TestCopyRectCrossPage`
- [x] 8. **全描画コマンドのページ引数化**: `draw_pixel`, `clear_vram`, `blit_rect`, `blit_rect_transform`, `copy_rect`, `read_rect` のフォーマット変更
- [x] 9. **blit_rect/blit_rect_transform から src_page 廃止**: ブレンド背景を dst_page から読む
- [x] 10. **ビルド確認**: `./scripts/process/build.sh`
- [x] 11. **vram_test.go にサイズ管理テスト追加**: `TestPageSizeManagement`, `TestCopyPageResizes`, `TestCopyRectCrossPageClipping`
- [x] 12. **handleSetPageSize 実装**: pageBuffer の width/height とバッファ再確保
- [x] 13. **ページ間操作のサイズ差異対応**: copy_page でリサイズ、copy_rect で各ページのサイズでクリッピング
- [x] 14. **ビューポート実装**: `handleSetViewport`, `ViewportOffset()`, `TestViewportOffset`
- [x] 15. **VRAMAccessor メソッド更新**: `VRAMBuffer`, `VRAMColorBuffer` が `pages[displayPage]` を返すよう変更
- [x] 16. **ビルド確認**: `./scripts/process/build.sh`
- [x] 17. **monitor_test.go にテスト追加**: `TestMonitorDirectRefresh`, `TestMonitorPageFilter`
- [x] 18. **Monitor の VRAMAccessor 型付け**: `any` → `VRAMAccessor` インターフェース
- [x] 19. **Monitor palette を [4]uint8 に拡張**: buildFrame で DirectColorMarker 対応
- [x] 20. **Monitor receiveLoop 拡張**: ページフィルタリング、拡張イベントハンドラ
- [x] 21. **refreshFromVRAM 実装**: ビューポート考慮の直接リフレッシュ
- [x] 22. **統合テスト追加**: `vram_page_test.go`
- [x] 23. **ビルド確認**: `./scripts/process/build.sh`
- [x] 24. **統合テスト確認**: ビルドパイプラインで全テスト通過確認済

## Verification Plan

### Automated Verification

1. **Build & Unit Tests**:
   ```bash
   ./scripts/process/build.sh
   ```
   - vram パッケージの全テスト（Part 1 + Part 2）がパスすること
   - monitor パッケージの全テスト（既存 + 新規）がパスすること

2. **Integration Tests**:
   ```bash
   ./scripts/process/integration_test.sh --specify TestPageManagementIntegration
   ./scripts/process/integration_test.sh --specify TestPageDrawIsolationIntegration
   ./scripts/process/integration_test.sh --specify TestPageDisplayIntegration
   ```
   - 既存テスト（`TestDemoProgram`, `TestHTTPStatsIntegration` 等）が通ること

## Documentation

#### [MODIFY] [prompts/phases/000-foundation/plans/main/016-VRAMFeatureReintegration-Part2.md](file://prompts/phases/000-foundation/plans/main/016-VRAMFeatureReintegration-Part2.md)
* **更新内容**: 各ステップの `[ ]` → `[x]` で進捗管理

## 継続計画について

Part 3 で以下を実装する:
- CPU デモ 7 シーン（シーンシステム + ヘルパー関数群）
- `cmd/main.go` の ChannelBus + TCPBridge 統合、`--no-tcp` フラグ、`OnClose` コールバック
