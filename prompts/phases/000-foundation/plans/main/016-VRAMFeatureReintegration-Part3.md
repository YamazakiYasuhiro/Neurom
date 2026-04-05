# 016-VRAMFeatureReintegration-Part3

> **Source Specification**: `prompts/phases/000-foundation/ideas/main/016-VRAMFeatureReintegration.md`

## Goal Description

CPU デモプログラムを7シーン構成のデモシステムに拡張し、`cmd/main.go` を ChannelBus + TCPBridge 構成に移行する。各シーンは VRAM 拡張コマンド（Part 1）、ページ機能（Part 2）を活用したデモンストレーションを行う。現在のパレットアニメーションデモは Scene 1 に統合する。

## User Review Required

- Scene 7（LargeMapScroll）は `set_page_size` で 512x512 のページを使用するため、Part 2 の完了が前提。
- `--no-tcp` フラグ追加により、TCPBridge なしでの起動が可能になる（デフォルトは TCP あり）。
- `--cpu N` フラグ追加（014-VRAMMultiCore）。Part 2 完了後に 014 を実施し、本 Part 3 で main.go に統合する。

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
|:---|:---|
| R6: --no-tcp フラグ | Proposed Changes > cmd/main.go — flag 追加 |
| R6: --cpu N フラグ (014) | Proposed Changes > cmd/main.go — flag 追加 + vram.New(Config) |
| R6: ChannelBus + TCPBridge | Proposed Changes > cmd/main.go — bus 構成変更 |
| R6: VRAMAccessor 接続 | Proposed Changes > cmd/main.go — mon.SetVRAMAccessor |
| R6: OnClose シャットダウン | Proposed Changes > cmd/main.go — MonitorConfig.OnClose |
| R7: Scene 1 (Palette+Clear) | Proposed Changes > cpu.go — scene1Init/Update |
| R7: Scene 2 (BlitRect) | Proposed Changes > cpu.go — scene2Init/Update |
| R7: Scene 3 (Transform) | Proposed Changes > cpu.go — scene3Init/Update |
| R7: Scene 4 (AlphaBlend) | Proposed Changes > cpu.go — scene4Init/Update |
| R7: Scene 5 (Scroll) | Proposed Changes > cpu.go — scene5Init/Update |
| R7: Scene 6 (PageComposite) | Proposed Changes > cpu.go — scene6Init/Update |
| R7: Scene 7 (LargeMapScroll) | Proposed Changes > cpu.go — scene7Init/Update |
| R7: ヘルパー関数群 | Proposed Changes > cpu.go — publish* 関数 |
| R7: ticker 8ms | Proposed Changes > cpu.go — run() 内 ticker |
| R7: シーン遷移 | Proposed Changes > cpu.go — sceneTable + 自動進行 |
| R7: シャットダウン sync.WaitGroup | cpu.go — 既存の wg を継続使用 |

## Proposed Changes

### cpu モジュール

#### [MODIFY] [features/neurom/internal/modules/cpu/cpu.go](file://features/neurom/internal/modules/cpu/cpu.go)

* **Description**: シーン回転型デモシステムを実装する。既存のパレットアニメーションは Scene 1 に統合。
* **Technical Design**:

  **定数と型:**
  ```go
  const (
      SceneDuration = 3 * time.Second   // 各シーンの表示時間
      TickInterval  = 8 * time.Millisecond // ~120FPS
      VRAMWidth     = 256
      VRAMHeight    = 212
  )

  type scene struct {
      name   string
      init   func(c *CPUModule, b bus.Bus)
      update func(c *CPUModule, b bus.Bus, frame int)
  }
  ```

  **CPUModule 構造体への追加フィールド:**
  ```go
  type CPUModule struct {
      wg       sync.WaitGroup
      frame    int    // 現在シーン内のフレーム番号
      sceneIdx int    // 現在のシーンインデックス
  }
  ```

  **run() のフロー:**
  ```go
  func (c *CPUModule) run(ctx context.Context, b bus.Bus, sysCh <-chan *bus.BusMessage) {
      // 1. VRAM モード初期化
      // 2. time.Sleep(300ms) — 初期化待ち
      // 3. シーンテーブル定義
      // 4. 現在シーンの init() 実行
      // 5. ticker (8ms) ループ:
      //    - ctx.Done / sysCh shutdown チェック
      //    - 現在シーンの update(frame) 実行
      //    - frame++
      //    - SceneDuration 経過で次シーンの init() 実行、frame=0
      //    - 最終シーン後は sceneIdx=0 にループ
  }
  ```

  **シーンテーブル:**
  ```go
  scenes := []scene{
      {"Palette+Clear",   scene1Init, scene1Update},
      {"BlitRect",        scene2Init, scene2Update},
      {"Transform",       scene3Init, scene3Update},
      {"AlphaBlend",      scene4Init, scene4Update},
      {"Scroll",          scene5Init, scene5Update},
      {"PageComposite",   scene6Init, scene6Update},
      {"LargeMapScroll",  scene7Init, scene7Update},
  }
  ```

  **ヘルパー関数群（Bus メッセージ生成・送信）:**
  ```go
  func (c *CPUModule) publishClearVRAM(b bus.Bus, page, paletteIdx uint8)
  // data: [page:u8][palette_idx:u8]

  func (c *CPUModule) publishBlitRect(b bus.Bus, page uint8, x, y, w, h uint16, blendMode uint8, pixels []uint8)
  // data: [page:u8][x_hi:u8][x_lo:u8][y_hi:u8][y_lo:u8][w_hi:u8][w_lo:u8][h_hi:u8][h_lo:u8][blend:u8][pixels...]

  func (c *CPUModule) publishBlitRectTransform(b bus.Bus, page uint8, x, y, srcW, srcH, pivotX, pivotY uint16, rotation uint8, scaleX, scaleY uint16, blendMode uint8, pixels []uint8)
  // data: [page:u8][x:u16][y:u16][srcW:u16][srcH:u16][pivotX:u16][pivotY:u16][rotation:u8][scaleX:u16][scaleY:u16][blend:u8][pixels...]

  func (c *CPUModule) publishCopyRect(b bus.Bus, srcPage, dstPage uint8, srcX, srcY, dstX, dstY, w, h uint16)
  // data: [src_page:u8][dst_page:u8][srcX:u16][srcY:u16][dstX:u16][dstY:u16][w:u16][h:u16]

  func (c *CPUModule) publishSetPaletteBlock(b bus.Bus, start, count uint8, colors [][4]uint8)
  // data: [start:u8][count:u8][R:u8][G:u8][B:u8][A:u8]... (count回)

  func (c *CPUModule) publishSetPageCount(b bus.Bus, count uint8)
  // data: [count:u8]

  func (c *CPUModule) publishSetDisplayPage(b bus.Bus, page uint8)
  // data: [page:u8]

  func (c *CPUModule) publishSwapPages(b bus.Bus, page1, page2 uint8)
  // data: [page1:u8][page2:u8]

  func (c *CPUModule) publishCopyPage(b bus.Bus, src, dst uint8)
  // data: [src:u8][dst:u8]

  func (c *CPUModule) publishSetPageSize(b bus.Bus, page uint8, w, h uint16)
  // data: [page:u8][w_hi:u8][w_lo:u8][h_hi:u8][h_lo:u8]

  func (c *CPUModule) publishSetViewport(b bus.Bus, offX, offY int16)
  // data: [offX_hi:u8][offX_lo:u8][offY_hi:u8][offY_lo:u8]
  ```

* **Logic**:

  **Scene 1: Palette+Clear**
  - `init`: `publishSetPaletteBlock` で 0〜255 番に HSV グラデーションパレットを設定（`hsvToRGB` を使用）。全色 A=255。
  - `update`: `publishClearVRAM(page=0, paletteIdx=(frame % 256))` で毎フレーム背景色を変更。HSV サイクリングアニメーション。
  - 既存のパレットアニメーションをこのシーンに統合。

  **Scene 2: BlitRect**
  - `init`: 8x8 チェッカーパターンのピクセルデータを生成（偶数マス = idx 1、奇数マス = idx 2）。`publishSetPaletteBlock` で使用パレットを設定。`publishClearVRAM` で画面クリア。
  - `update`: 画面全体にチェッカーパターンを `publishBlitRect`（Replace モード）でタイル状に配置。`frame` に応じてパレット色を変更してアニメーション。
    - タイル数: (256/8) x (212/8) = 32 x 26 タイル
    - 各フレームでパレット idx 1, 2 の色をシフト

  **Scene 3: Transform**
  - `init`: 8x8 ダイヤモンド形状（中心がインデックス値、外周が 0）のスプライトデータを定義。パレット設定。`publishClearVRAM` で画面クリア。
  - `update`:
    - 画面中央 (128, 106): 中心 pivot (4,4) で回転アニメーション。rotation = `(frame * 2) % 256`。等倍。
    - 画面左 (64, 106): 足元 pivot (4,7) で同じ回転。等倍。
    - 画面右 (192, 106): 中心 pivot (4,4) で同じ回転。2倍拡大 (scaleX=0x0200, scaleY=0x0200)。
    - 毎フレーム `publishClearVRAM` で背景をクリアしてから3つのスプライトを描画。

  **Scene 4: AlphaBlend**
  - `init`: パレット設定 — idx 1=(255,0,0,255) 赤、idx 2=(0,255,0,255) 緑、idx 3=(0,0,255,255) 青、idx 10=(255,255,255,128) 白半透明。`publishClearVRAM` で画面クリア。
    - 赤・緑・青の3本の縦バー（各幅 80 ピクセル程度）を `publishBlitRect` Replace で描画。
  - `update`:
    - 5つのブレンドモード（Replace=0, Alpha=1, Additive=2, Multiply=3, Screen=4）で idx 10 の 40x40 矩形を横に並べて描画。
    - Y 位置を `frame` に応じてアニメーション（バウンス）。

  **Scene 5: Scroll**
  - `init`: パレット設定（カラフルな横縞用）。画面全体にカラフルな横縞パターンを `publishBlitRect` で描画。各行のインデックスは `y % 16 + 1`。
  - `update`:
    - `publishCopyRect` で画面全体を Y-1 にコピー（上方向スクロール）: srcPage=0, dstPage=0, (0,1) → (0,0), w=256, h=211。
    - 最下行 (y=211) を新しいパターンで埋める: `publishBlitRect` で 256x1 の行データを書き込み。パターンは `(frame + y) % 16 + 1`。

  **Scene 6: PageComposite**
  - `init`:
    - `publishSetPageCount(2)` でページ2枚を確保。
    - ページ 0: 背景レイヤー。横縞パターンを描画。
    - ページ 1: フォアグラウンドレイヤー。初期化時はクリア。
  - `update`:
    - ページ 0: 背景を `publishCopyRect` で横スクロール（左方向）。右端列を新パターンで埋める。
    - ページ 1: `publishClearVRAM` でクリア。8x8 ダイヤモンドスプライトを `publishBlitRect` でバウンシング描画。
    - `publishCopyRect(srcPage=1, dstPage=0, ...)` でページ 1 の内容をページ 0 に合成。
    - `publishSetDisplayPage(0)` でページ 0 を表示。
  - シーン終了時: `publishSetPageCount(1)` でページ数をリセット。

  **Scene 7: LargeMapScroll**
  - `init`:
    - `publishSetPageCount(2)` でページ2枚を確保（ダブルバッファ用）。
    - `publishSetPageSize(0, 512, 512)` でページ 0 を 512x512 に拡大。
    - パレット設定（グラデーション用）。
    - ページ 0 全体（512x512）にタイルマップパターンを生成・描画: `publishBlitRect` で色付きタイルを配置。タイルのインデックスは位置に応じた幾何学パターン。
    - ページ 1 は表示用（デフォルト 256x212）。
  - `update`:
    - リサジュー曲線で表示オフセットを計算:
      ```
      offsetX = int(128 + 128*sin(frame * 0.02))
      offsetY = int(106 + 106*sin(frame * 0.03))
      ```
    - `publishSetViewport(offsetX, offsetY)` でビューポートをスクロール。
    - 表示ページはページ 0（大マップ）。
  - シーン終了時:
    - `publishSetViewport(0, 0)` でビューポートリセット。
    - `publishSetPageSize(0, 256, 212)` でページサイズリセット。
    - `publishSetPageCount(1)` でページ数リセット。

  **シーン遷移共通処理:**
  - シーン切替前に `publishClearVRAM(0, 0)` で画面クリア。
  - シーン切替前に `publishSetPageCount(1)` でページ数を 1 にリセット（Scene 6, 7 で複数ページ使用後の後始末）。
  - `publishSetViewport(0, 0)` でビューポートリセット。
  - `publishSetDisplayPage(0)` で表示ページをリセット。

  **既存 hsvToRGB 関数**: 維持（Scene 1 で使用）。

### cmd/main.go

#### [MODIFY] [features/neurom/cmd/main.go](file://features/neurom/cmd/main.go)

* **Description**: ChannelBus + TCPBridge 構成に移行、`--no-tcp` フラグ追加、VRAMAccessor 接続、OnClose 統合
* **Technical Design**:

  **フラグ追加:**
  ```go
  noTCP := flag.Bool("no-tcp", false, "Disable TCP bridge for external connections")
  cpuWorkers := flag.Int("cpu", 1, "Number of VRAM worker goroutines (1-256)")
  ```

  **Bus 構成の変更:**
  ```go
  // ZMQBus → ChannelBus + TCPBridge
  channelBus := bus.NewChannelBus()
  var tcpBridge *bus.TCPBridge

  if !*noTCP {
      tcpBridge = bus.NewTCPBridge(bus.TCPBridgeConfig{
          Bus:         channelBus,
          TCPEndpoint: "tcp://127.0.0.1:" + *tcpPort,
      })
      if err := tcpBridge.Start(); err != nil {
          log.Printf("TCP bridge failed to start: %v", err)
      }
  }
  ```

  **MonitorConfig.OnClose:**
  ```go
  mon := monitor.New(monitor.MonitorConfig{
      Headless: *headless,
      OnClose:  cancel,  // ウィンドウ閉じ時に context をキャンセル
  })
  ```

  **VRAM モジュール生成（Config 付き）:**
  ```go
  vramMod := vram.New(vram.Config{Workers: *cpuWorkers})
  ```

  **VRAMAccessor 接続:**
  ```go
  mon.SetVRAMAccessor(vramMod) // VRAMModule は VRAMAccessor を満たす
  ```

  **シャットダウン時の TCPBridge クローズ:**
  ```go
  defer func() {
      if tcpBridge != nil {
          tcpBridge.Close()
      }
      channelBus.Close()
  }()
  ```

  **Manager への Bus 渡し:**
  ```go
  mgr := module.NewManager(channelBus)  // ChannelBus を渡す
  ```

* **Logic**:
  - `bus.NewZMQBus(...)` の呼び出しを `bus.NewChannelBus()` に置き換える
  - `--no-tcp` が false（デフォルト）の場合のみ `TCPBridge` を起動する
  - `vram.New()` → `vram.New(vram.Config{Workers: *cpuWorkers})` に変更（014-VRAMMultiCore 統合）
  - `mon.SetVRAMAccessor(vramMod)` で Monitor → VRAM の直接参照を確立
  - `MonitorConfig.OnClose` に `cancel` を設定し、ウィンドウ閉じ時に `context.Cancel()` を呼び出す
  - Monitor の `RunMain` 内で `StageDead` 検出時に `OnClose()` を呼び出す（monitor.go 側で対応が必要な場合は Part 2 で実装済み）

### 統合テスト

#### [MODIFY] [features/neurom/integration/vram_enhancement_test.go](file://features/neurom/integration/vram_enhancement_test.go)

* **Description**: `TestDemoProgram` を7シーンのフルデモ用に更新
* **Technical Design**:
  ```go
  func TestDemoProgram(t *testing.T)
  // - ChannelBus + Manager で CPU + VRAM + Monitor(headless) を起動
  // - 25秒間（7シーン × 3秒 + バッファ）実行
  // - パニックやデッドロックが発生しないことを確認
  // - 各シーン遷移でログメッセージを確認（オプション）
  ```

## Step-by-Step Implementation Guide

- [x] 1. **CPU ヘルパー関数の実装**: `publishClearVRAM`, `publishBlitRect`, `publishBlitRectTransform`, `publishCopyRect`, `publishSetPaletteBlock`, `publishSetPageCount`, `publishSetDisplayPage`, `publishSwapPages`, `publishCopyPage`, `publishSetPageSize`, `publishSetViewport`
- [x] 2. **シーンシステムの実装**: `scene` 型定義、`run()` メソッドのリファクタリング（シーンテーブル + 自動進行ループ）
- [x] 3. **Scene 1 (Palette+Clear) の実装**: 既存パレットアニメーションを `scene1Init`/`scene1Update` に移行。`set_palette_block` + `clear_vram` を使用。
- [x] 4. **ビルド確認**: `./scripts/process/build.sh` — Scene 1 のみで動作確認
- [x] 5. **Scene 2 (BlitRect) の実装**: チェッカーパターン生成 + `blit_rect` 描画
- [x] 6. **Scene 3 (Transform) の実装**: ダイヤモンドスプライト + `blit_rect_transform` 回転/拡大
- [x] 7. **Scene 4 (AlphaBlend) の実装**: 3色バー + 5ブレンドモード表示
- [x] 8. **Scene 5 (Scroll) の実装**: 横縞パターン + `copy_rect` スクロール
- [x] 9. **ビルド確認**: `./scripts/process/build.sh` — Scene 1〜5 で動作確認
- [x] 10. **Scene 6 (PageComposite) の実装**: 2ページ構成 + レイヤー合成
- [x] 11. **Scene 7 (LargeMapScroll) の実装**: 512x512 大マップ + リサジュースクロール
- [x] 12. **シーン遷移リセット処理の実装**: ページ数/ビューポート/表示ページの初期化
- [x] 13. **ビルド確認**: `./scripts/process/build.sh` — 全7シーンのビルド成功確認
- [x] 14. **cmd/main.go の修正**: ChannelBus + TCPBridge 構成、`--no-tcp` フラグ、`--cpu N` フラグ、VRAMAccessor 接続、OnClose
- [x] 15. **ビルド確認**: `./scripts/process/build.sh`
- [x] 16. **統合テスト更新**: `TestDemoProgram` を7シーン対応に更新
- [x] 17. **統合テスト確認**: ビルドパイプラインで全シーン遷移確認済
- [x] 18. **全テスト確認**: `./scripts/process/build.sh` 全パス確認済

## Verification Plan

### Automated Verification

1. **Build & Unit Tests**:
   ```bash
   ./scripts/process/build.sh
   ```
   - 全パッケージのテストがパスすること（vram, monitor, cpu）

2. **Integration Tests**:
   ```bash
   ./scripts/process/integration_test.sh --specify TestDemoProgram
   ./scripts/process/integration_test.sh
   ```
   - `TestDemoProgram`: 7シーンが25秒間パニックなしで実行されること
   - 全統合テスト（Part 1, Part 2 含む）がリグレッションなく通ること
   - **Log Verification**: 各シーン切替時に `[CPU] Scene N: {SceneName}` のログが出力されること

## Documentation

#### [MODIFY] [prompts/phases/000-foundation/plans/main/016-VRAMFeatureReintegration-Part3.md](file://prompts/phases/000-foundation/plans/main/016-VRAMFeatureReintegration-Part3.md)
* **更新内容**: 各ステップの `[ ]` → `[x]` で進捗管理

#### [MODIFY] [features/README.md](file://features/README.md)
* **更新内容**: `--no-tcp` フラグ、`--cpu N` フラグの説明を追加。CPU デモの7シーン構成について説明。
