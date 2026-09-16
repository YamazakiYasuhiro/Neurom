# 009-VRAMEventSync

> **Source Specification**: prompts/phases/000-foundation/ideas/main/009-VRAMEventSync.md

## Goal Description

Monitor モジュールの VRAM 同期方式を2モード化する。(1) 同一プロセスでは Setter 注入によるダイレクトアクセス（タイマー駆動リフレッシュ）、(2) リモート/Bus モードでは header-only 廃止と `rect_copied` 専用イベント追加による完全同期。これにより、全操作（大きな blit_rect、copy_rect スクロール等）が Monitor に正しく反映される。

## User Review Required

None.

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| 1. ダイレクトアクセスモード（タイマー駆動、Bus 非依存） | Proposed Changes > monitor > `SetVRAMAccessor`, `directRefreshLoop`, `refreshFromVRAM` |
| 2. Bus モード改善（header-only 廃止 + rect_copied） | Proposed Changes > vram > `publishRectEvent` 変更, `handleCopyRect` 変更 / monitor > `rect_copied` ハンドリング |
| 3. Consumer Driven インターフェース | Proposed Changes > monitor > `VRAMAccessor` interface |
| 4. Setter による注入 | Proposed Changes > cmd > main.go |
| 5. Monitor 内部コピー廃止（ダイレクト時） | Proposed Changes > monitor > `handleVRAMUpdate` でダイレクト時スキップ, `GetPixel` 変更 |
| 6. 既存テストの維持 | Verification Plan > 全テスト PASS 確認 |
| 7. リフレッシュレート設定（任意） | Proposed Changes > monitor > `MonitorConfig.RefreshRate` |
| 8. モード検出ログ（任意） | Proposed Changes > monitor > `Start()` 内ログ出力 |

## Proposed Changes

### vram (accessor + bus improvements)

#### [MODIFY] [features/neurom/internal/modules/vram/vram_test.go](file://features/neurom/internal/modules/vram/vram_test.go)

*   **Description**: VRAM アクセサメソッドの単体テストを追加
*   **Technical Design**:
    *   `TestVRAMAccessorValues`: `New()` で生成した VRAMModule に対し、`VRAMBuffer()` が `len == width*height` のスライスを返すこと、`VRAMColorBuffer()` が `len == width*height*4` のスライスを返すこと、`VRAMWidth() == 256`, `VRAMHeight() == 212` であることを検証
    *   `draw_pixel` でピクセルを書き込んだ後、`VRAMBuffer()` の該当インデックスが更新されていることを確認
    *   `RLock()` / `RUnlock()` がパニックしないことを確認

#### [MODIFY] [features/neurom/internal/modules/vram/vram.go](file://features/neurom/internal/modules/vram/vram.go)

*   **Description**: VRAMAccessor 準拠のアクセサメソッド追加、`publishRectEvent` の header-only 廃止、`handleCopyRect` に `rect_copied` イベント追加
*   **Technical Design**:

    アクセサメソッド（7つ）:
    ```go
    func (v *VRAMModule) RLock()                      // v.mu.RLock() を公開
    func (v *VRAMModule) RUnlock()                    // v.mu.RUnlock() を公開
    func (v *VRAMModule) VRAMBuffer() []uint8         // v.buffer を返す
    func (v *VRAMModule) VRAMColorBuffer() []uint8    // v.colorBuffer を返す
    func (v *VRAMModule) VRAMPalette() [256][4]uint8  // v.palette を返す
    func (v *VRAMModule) VRAMWidth() int              // v.width を返す
    func (v *VRAMModule) VRAMHeight() int             // v.height を返す
    ```

*   **Logic**:

    `publishRectEvent` の変更:
    - `maxCompactEventPixels` 定数と `maxFullEventPixels` 定数を削除
    - サイズ分岐を撤廃し、常に full フォーマット（mode=0x01: header + index + RGBA）を送信
    - full フォーマットの構造: `[x:u16][y:u16][w:u16][h:u16][mode=0x01:u8][index_data:w*h bytes][rgba_data:w*h*4 bytes]`

    `handleCopyRect` の変更:
    - 既存の `publishRectEvent(cx, cy, cw, ch)` 呼び出しを `publishCopyEvent(...)` に置換
    - ソース座標はクリッピングオフセットを加算: `publishCopyEvent(srcX+srcOffX, srcY+srcOffY, cx, cy, cw, ch)`
    - 新メソッド `publishCopyEvent(srcX, srcY, dstX, dstY, w, h int)`: `rect_copied` イベントを `vram_update` トピックに発行
    - フォーマット: `[src_x:u16][src_y:u16][dst_x:u16][dst_y:u16][w:u16][h:u16]` (12バイト固定)

---

### monitor (dual access mode)

#### [MODIFY] [features/neurom/internal/modules/monitor/monitor.go](file://features/neurom/internal/modules/monitor/monitor.go)

*   **Description**: VRAMAccessor インターフェース定義、SetVRAMAccessor Setter、ダイレクトリフレッシュループ、Bus モード改善、GetPixel 変更
*   **Technical Design**:

    **インターフェース定義**:
    ```go
    type VRAMAccessor interface {
        RLock()
        RUnlock()
        VRAMBuffer() []uint8
        VRAMColorBuffer() []uint8
        VRAMPalette() [256][4]uint8
        VRAMWidth() int
        VRAMHeight() int
    }
    ```

    **MonitorModule 構造体への追加フィールド**:
    ```go
    vramAccessor VRAMAccessor // nil = Bus mode, non-nil = direct mode
    ```

    **MonitorConfig への追加フィールド**:
    ```go
    RefreshRate int // fps for direct mode (default 30)
    ```

    **Setter**:
    ```go
    func (m *MonitorModule) SetVRAMAccessor(v VRAMAccessor)
    // m.vramAccessor = v を設定
    ```

    **directRefreshLoop(ctx context.Context)**:
    - `RefreshRate` が 0 の場合 30 をデフォルトとする
    - `time.NewTicker(time.Second / time.Duration(rate))` でティッカー生成
    - ループ: ctx.Done / ticker.C を select
    - ticker.C 時: `refreshFromVRAM()` → `requestPaint()` を呼ぶ

    **refreshFromVRAM()**:
    - `vramAccessor.RLock()` → VRAM の `VRAMBuffer()`, `VRAMColorBuffer()`, `VRAMPalette()` を取得
    - `m.mu.Lock()` → `m.rgba` にピクセルデータを直接構築
    - パレットインデックスが `0xFF` の場合は `VRAMColorBuffer()` から RGBA を読み、それ以外は `VRAMPalette()` からルックアップ
    - `m.dirty = true` を設定（`onPaint` でテクスチャアップロードをトリガー）
    - ロック解放

*   **Logic**:

    **Start() の変更**:
    - `vramAccessor != nil` の場合:
      - ログ出力: `"[Monitor] Start: direct access mode (refresh rate: %d fps)"`
      - `directRefreshLoop` goroutine を起動（`m.wg.Add(1)` で管理）
    - `vramAccessor == nil` の場合:
      - ログ出力: `"[Monitor] Start: bus event mode"`
      - 従来通り `receiveLoop` goroutine のみ起動
    - **注**: ダイレクトモードでも `receiveLoop` は起動する（system メッセージの受信が必要なため）。ただし `handleVRAMUpdate` 内で vram 同期系イベントをスキップする

    **handleVRAMUpdate の変更**:
    - 先頭にガード追加: `if m.vramAccessor != nil { return }` — ダイレクトモードでは Bus の VRAM 同期イベントを全て無視
    - `rect_copied` ケースを追加（Bus モード時のみ実行される）:
      - データから `srcX, srcY, dstX, dstY, w, h` をパース（各 u16）
      - `m.vram`, `m.directRGBA`, `m.hasDirectPixel` に対して一時バッファ経由のコピーを実行（`handleCopyRect` と同じロジック）
      - `m.dirty = true` → `requestPaint()`

    **GetPixel の変更**:
    - ダイレクトモード時: `refreshFromVRAM()` を呼んで最新データを `m.rgba` に反映してから読み取り
    - Bus モード時: 従来通り `m.dirty` チェック → `buildFrame()` → `m.rgba` から読み取り
    - ロック順序: `refreshFromVRAM()` は内部で vramAccessor.RLock → m.mu.Lock の順でロックするため、`GetPixel` では `refreshFromVRAM()` を `m.mu.Lock()` の**外**で呼ぶ

    **onPaint の変更**:
    - 現在の `onPaint` は `m.dirty == true` 時に `buildFrame()` を呼んでから `TexImage2D` でアップロードする
    - ダイレクトモードでは `refreshFromVRAM()` が `m.rgba` を直接構築済みのため、`buildFrame()` を呼ぶと `m.vram`（ダイレクトモードでは未更新）から上書きされて表示が壊れる
    - 修正: `m.dirty == true` のブロック内で、`m.vramAccessor == nil`（Bus モード）の場合のみ `buildFrame()` を呼ぶ。ダイレクトモードでは `m.rgba` がすでに最新なのでテクスチャアップロードのみ実行

---

### integration (new tests)

#### [NEW] [features/neurom/integration/monitor_sync_test.go](file://features/neurom/integration/monitor_sync_test.go)

*   **Description**: ダイレクトモードと Bus モードの Monitor 同期をエンドツーエンドで検証する統合テスト
*   **Technical Design**:

    共通ヘルパー:
    ```go
    // setupModules creates VRAM + Monitor (headless), optionally wiring direct access
    func setupModules(t *testing.T, directMode bool) (
        b bus.Bus, mgr *module.Manager, mon *monitor.MonitorModule,
        ctx context.Context, cancel context.CancelFunc,
    )
    // directMode=true: mon.SetVRAMAccessor(vramMod) を呼ぶ
    // directMode=false: SetVRAMAccessor を呼ばない（Bus モード）
    ```

    ```go
    // publishAndWait sends a bus message and waits for propagation
    func publishAndWait(b bus.Bus, topic string, msg *bus.BusMessage, waitMs int)
    ```

    **テスト一覧**:

    `TestMonitorDirect_CopyRect`:
    1. `setupModules(t, true)` でダイレクトモード起動
    2. パレット設定: `set_palette` で index 10,20,30,40 に異なる色を設定
    3. Bus 経由で 4 行分の `blit_rect` を (0,10)〜(0,13) に行ごとに index 10,20,30,40 で描画
    4. Bus 経由で `copy_rect` src=(0,11) dst=(0,10) size=256×3（1行上スクロール）
    5. `time.Sleep(100ms)` でリフレッシュサイクルを待つ
    6. `mon.GetPixel(0, 10)` がパレット[20] の色であることを確認
    7. `mon.GetPixel(0, 11)` がパレット[30] の色であることを確認
    8. `mon.GetPixel(0, 12)` がパレット[40] の色であることを確認

    `TestMonitorDirect_LargeBlit`:
    1. `setupModules(t, true)` でダイレクトモード起動
    2. パレット設定: `set_palette` で index 5 に (100,200,50,255) を設定
    3. 256×212 全画面 `blit_rect(Replace)` を index 5 で描画
    4. `time.Sleep(100ms)`
    5. `mon.GetPixel(0,0)`, `GetPixel(128,106)`, `GetPixel(255,211)` が全て (100,200,50,255) であることを確認

    `TestMonitorDirect_AlphaBlend`:
    1. `setupModules(t, true)` でダイレクトモード起動
    2. パレット設定: index 1 = (255,0,0,255), index 2 = (0,0,255,128)
    3. 赤 4×4 を (20,20) に `blit_rect(Replace)` で描画
    4. 青 4×4 を (20,20) に `blit_rect(AlphaBlend)` で上書き
    5. `time.Sleep(100ms)`
    6. `mon.GetPixel(20,20)` が (R≈127, G=0, B≈128, A=255) ±1 であることを確認

    `TestMonitorBus_CopyRect`:
    1. `setupModules(t, false)` で Bus モード起動
    2. パレット設定 + 4 行 blit_rect + copy_rect（ダイレクトテストと同手順）
    3. `time.Sleep(200ms)` でイベント伝播を待つ
    4. `mon.GetPixel()` で同じ検証

    `TestMonitorBus_LargeBlit`:
    1. `setupModules(t, false)` で Bus モード起動
    2. 全画面 `blit_rect` を実行
    3. `time.Sleep(200ms)`
    4. `mon.GetPixel()` で検証

    `TestMonitorBus_SmallBlit`:
    1. `setupModules(t, false)` で Bus モード起動
    2. 小さな 4×4 `blit_rect` を実行（従来動作の確認）
    3. `time.Sleep(200ms)`
    4. `mon.GetPixel()` で検証

---

### cmd

#### [MODIFY] [features/neurom/cmd/main.go](file://features/neurom/cmd/main.go)

*   **Description**: VRAM モジュールを変数に保持し、Monitor に SetVRAMAccessor で注入
*   **Logic**:
    - `vram.New()` の戻り値を `vramMod` 変数に保持（現在は `mgr.Register(vram.New())` で直接渡している）
    - `mon := monitor.New(...)` の後、`mgr.Register(mon)` の前に `mon.SetVRAMAccessor(vramMod)` を呼び出す
    - `mgr.Register(vramMod)` で登録

## Step-by-Step Implementation Guide

### Phase 1: VRAM アクセサメソッド

- [x] **Step 1: VRAM アクセサメソッドとテスト**
    * `vram.go` に 7 つのアクセサメソッド (`RLock`, `RUnlock`, `VRAMBuffer`, `VRAMColorBuffer`, `VRAMPalette`, `VRAMWidth`, `VRAMHeight`) を追加
    * `vram_test.go` に `TestVRAMAccessorValues` を追加: バッファ長、ピクセル書き込み後の反映を検証
    * `./scripts/process/build.sh` で新テスト PASS を確認

### Phase 2: Monitor ダイレクトモード

- [x] **Step 2: VRAMAccessor インターフェースと SetVRAMAccessor**
    * `monitor.go` に `VRAMAccessor` インターフェースを追加
    * `MonitorModule` 構造体に `vramAccessor VRAMAccessor` フィールドを追加
    * `MonitorConfig` に `RefreshRate int` フィールドを追加
    * `SetVRAMAccessor(v VRAMAccessor)` メソッドを追加

- [x] **Step 3: ダイレクトリフレッシュ実装**
    * `refreshFromVRAM()` メソッドを追加: VRAMAccessor から読み取り → `m.rgba` に直接構築
    * `directRefreshLoop(ctx)` メソッドを追加: タイマー駆動で `refreshFromVRAM` + `requestPaint`
    * `Start()` を変更: `vramAccessor != nil` 時に `directRefreshLoop` goroutine を追加起動
    * `handleVRAMUpdate()` 先頭にガード追加: `if m.vramAccessor != nil { return }`
    * `GetPixel()` を変更: ダイレクトモード時は `refreshFromVRAM()` を呼んでから `m.rgba` を読み取り
    * `onPaint()` を変更: `m.dirty == true` 時、`m.vramAccessor == nil` の場合のみ `buildFrame()` を呼ぶ。ダイレクトモードでは `m.rgba` がすでに最新なのでテクスチャアップロードのみ

- [x] **Step 4: ダイレクトモード統合テスト** (build.sh PASS確認済)
    * `integration/monitor_sync_test.go` を新規作成
    * `setupModules` ヘルパーを作成
    * `TestMonitorDirect_CopyRect`, `TestMonitorDirect_LargeBlit`, `TestMonitorDirect_AlphaBlend` を追加
    * `./scripts/process/build.sh` で PASS を確認

### Phase 3: Bus モード改善

- [x] **Step 5: VRAM 側 Bus モード変更**
    * `publishRectEvent` から `maxCompactEventPixels`, `maxFullEventPixels` 定数を削除
    * `publishRectEvent` を簡略化: 常に full フォーマット（header + index + RGBA）を送信
    * `handleCopyRect` の末尾: `publishRectEvent` を `publishCopyEvent` に置換
    * `publishCopyEvent(srcX, srcY, dstX, dstY, w, h int)` を追加: `rect_copied` イベントを発行（12バイトの座標データ）
    * **既存テスト更新**: `TestLargeRectEvent` を更新
      - サブテスト "large blit_rect does not panic": mode=0x02（header-only）→ mode=0x01（full）に変更。データ長検証: 9 → `9 + 256*212 + 256*212*4` に変更
      - サブテスト "medium blit_rect sends compact format": mode=0x00（compact）→ mode=0x01（full）に変更。データ長検証: `9+1024` → `9 + 1024 + 1024*4` に変更。サブテスト名も "medium blit_rect sends full format" に変更
    * **既存テスト更新**: `TestScrollExact` のイベント検証部分を更新: `rect_updated` mode=0x02 の検証 → `rect_copied` イベント（12バイト座標データ）の検証に置換

- [x] **Step 6: Monitor 側 Bus モード変更**
    * `handleVRAMUpdate` に `case "rect_copied":` を追加
    * パース: データから `srcX, srcY, dstX, dstY, w, h` を u16 で読み取り
    * 一時バッファに `m.vram`, `m.directRGBA`, `m.hasDirectPixel` のソース領域をコピー
    * デスト領域にクリッピング付きで書き戻し
    * `m.dirty = true` → `requestPaint()`
    * 既存の `rect_updated` mode=0x02 (header-only) 分岐を削除

- [x] **Step 7: Bus モード統合テスト**
    * `TestMonitorBus_CopyRect`, `TestMonitorBus_LargeBlit`, `TestMonitorBus_SmallBlit` を追加
    * `./scripts/process/build.sh` で PASS を確認

### Phase 4: main.go 更新とリグレッション

- [x] **Step 8: main.go 更新**
    * `vramMod := vram.New()` で変数保持
    * `mon.SetVRAMAccessor(vramMod)` を呼び出し
    * `mgr.Register(vramMod)` で登録

- [x] **Step 9: 全テスト実行** (build.sh PASS)
    * `./scripts/process/build.sh` で全単体テスト + ビルド PASS を確認
    * 既存テスト（`TestBlitRectExact`, `TestTransformExact_*`, `TestAlphaBlendExact`, `TestScrollExact*`, `TestLargeRectEvent` 等）にリグレッションがないこと

- [x] **Step 10: 統合テスト実行** (build.sh 内で integration パッケージ PASS確認済)
    * `./scripts/process/integration_test.sh` で全統合テスト PASS を確認
    * 既存統合テスト（`TestVRAMMonitorIntegration`, `TestBlitAndReadRect`, `TestAlphaBlendPipeline` 等）にリグレッションがないこと

## Verification Plan

### Automated Verification

1.  **Build & Unit Tests**:
    ```bash
    ./scripts/process/build.sh
    ```
    *   **確認項目**:
        - 新規テスト: `TestVRAMAccessorValues` PASS
        - 新規テスト: `TestMonitorDirect_CopyRect`, `TestMonitorDirect_LargeBlit`, `TestMonitorDirect_AlphaBlend` PASS
        - 新規テスト: `TestMonitorBus_CopyRect`, `TestMonitorBus_LargeBlit`, `TestMonitorBus_SmallBlit` PASS
        - 既存テスト: `TestBlitRectExact`, `TestTransformExact_*`, `TestAlphaBlendExact`, `TestScrollExact*` 等リグレッションなし
        - **特に確認**: `TestLargeRectEvent` — `publishRectEvent` のフォーマット変更で影響を受ける可能性あり。mode=0x02, mode=0x00 の分岐テストは新しい常時 full フォーマットに合わせて更新が必要

2.  **Integration Tests**:
    ```bash
    ./scripts/process/integration_test.sh
    ```
    *   **確認項目**:
        - 新規: `TestMonitorDirectSync` 系、`TestMonitorBusSync` 系が全て PASS
        - 既存: `TestVRAMMonitorIntegration`, `TestBlitAndReadRect`, `TestAlphaBlendPipeline`, `TestDemoProgram` リグレッションなし

## Documentation

本計画の範囲ではドキュメント更新は不要。ただし、既存テスト `TestLargeRectEvent` はイベントフォーマットの変更に伴い期待値の更新が必要（compact mode=0x00, header-only mode=0x02 のテストケースを full mode=0x01 に変更）。
