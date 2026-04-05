# 016: VRAM機能再統合とCPUデモ復元

## 背景 (Background)

過去の開発セッション（006-VRAMEnhancement, 012-VRAMPage, 013-VRAMViewport）で実装されたVRAM拡張機能群とCPUデモプログラムが、`git reset` により未コミットの変更として失われた。

### 現在の状態

| コンポーネント | コミット済み状態 | 残存する未追跡ファイル | 失われた変更 |
|---|---|---|---|
| `vram.go` | 基本コマンドのみ（`mode`, `draw_pixel`, `set_palette`）+ stats計測 | — | 拡張コマンドハンドラ全体、二重バッファ構造、ページ管理、ビューポート |
| `blend.go` | — | ✅ 完全に残存 | — |
| `blend_test.go` | — | ✅ 完全に残存 | — |
| `transform.go` | — | ✅ 完全に残存 | — |
| `transform_test.go` | — | ✅ 完全に残存 | — |
| `cpu.go` | パレットアニメーションのみ（単一デモ） | — | 7シーンのデモシステム、ヘルパー関数群 |
| `monitor.go` | Bus受信による描画 + stats計測 | — | VRAMAccessor直接参照、拡張イベントハンドラ、ページフィルタリング |
| `cmd/main.go` | 基本モジュール起動 + stats-port | — | `--no-tcp` フラグ、OnClose統合、VRAMAccessor接続 |

### 残存する実装資産

以下のファイルは未追跡（untracked）として作業ディレクトリに残っており、そのまま活用できる:

- `features/neurom/internal/modules/vram/blend.go` — 5つのブレンドモード実装（Replace, Alpha, Additive, Multiply, Screen）
- `features/neurom/internal/modules/vram/blend_test.go` — ブレンドモードの単体テスト
- `features/neurom/internal/modules/vram/transform.go` — アフィン変換（回転・拡大）の逆変換方式実装
- `features/neurom/internal/modules/vram/transform_test.go` — 変換の単体テスト

### 参照する既存仕様

- `006-VRAMEnhancement.md` — VRAM拡張コマンド、二重バッファ構造、ブレンド描画、デモシーン1〜5の仕様
- `012-VRAMPage.md` — ページデータ構造、ページ管理コマンド、デモシーン6（PageComposite）
- `013-VRAMViewport.md` — ページ単位サイズ管理、ビューポートオフセット、デモシーン7（LargeMapScroll）

## 要件 (Requirements)

### 必須要件

本仕様は、上記3つの既存仕様の要件を再統合する形で実現する。個々の要件の詳細は各仕様書を参照すること。

#### R1: VRAM二重バッファ構造の復元

- `VRAMModule` に以下のバッファ構造を導入する:
  - **パレットインデックスバッファ** (`indexBuffer []uint8`): パレットインデックスを保持
  - **直接色バッファ** (`colorBuffer []uint8`, RGBA形式): ブレンド結果をRGBA値で保持
- パレットを `[256][3]uint8` (RGB) から `[256][4]uint8` (RGBA) へ拡張する
- 既存の `buffer []uint8` は `indexBuffer` にリネームする
- 詳細: `006-VRAMEnhancement.md` REQ-1, 3.2節

#### R2: 拡張VRAMコマンドの復元

`vram.go` の `handleMessage` に以下のコマンドハンドラを追加する:

| コマンド | 概要 | 参照仕様 |
|---|---|---|
| `clear_vram` | 指定パレットインデックスでVRAM全体を塗りつぶし | 006 REQ-6 |
| `blit_rect` | 矩形領域へのピクセル書き込み（ブレンドモード付き） | 006 REQ-2, REQ-9 |
| `blit_rect_transform` | 回転・拡大付き矩形書き込み | 006 REQ-3 |
| `read_rect` | 矩形領域のインデックスデータ読み出し | 006 REQ-4 |
| `copy_rect` | VRAM内矩形コピー（重複領域安全） | 006 REQ-5 |
| `set_palette_block` | パレット一括書き込み | 006 REQ-7 |
| `read_palette_block` | パレット一括読み出し | 006 REQ-8 |

- 全操作でVRAM境界クリッピングを適用する（006 REQ-10）
- `blend.go` の `BlendPixel` 関数を `blit_rect`/`blit_rect_transform` のブレンド処理に使用
- `transform.go` の `TransformBlit` 関数を `blit_rect_transform` の変換処理に使用
- 各コマンド完了後に適切なイベント（`vram_updated`, `vram_cleared`, `rect_updated`, `rect_copied` 等）を発行する
- 既存の `draw_pixel`, `set_palette`, `mode`, `get_stats` コマンドは維持する

#### R3: VRAMページ機能の復元

- `pageBuffer` 構造体の導入（各ページが独立した `indexBuffer` + `colorBuffer` を保持）
- ページ管理コマンドの追加: `set_page_count`, `set_display_page`, `swap_pages`, `copy_page`
- 全描画コマンドにページ引数を追加（ワイヤーフォーマットの先頭バイトに `page:u8` を追加）
- 詳細: `012-VRAMPage.md` R1〜R6

#### R4: ページ単位サイズ管理とビューポートの復元

- `pageBuffer` に独立した `width`/`height` を追加
- `set_page_size` コマンドの追加
- `VRAMModule` のグローバル `width`/`height` をデフォルトページサイズ定数に変更
- `ViewportOffset()` メソッドの実装
- 詳細: `013-VRAMViewport.md` R1〜R4, R10

#### R5: Monitorモジュールの拡張描画パイプライン復元

- `VRAMAccessor` インターフェースの定義と実装（Monitor → VRAM 直接参照）
- 拡張イベントハンドラ: `rect_updated`（full/compact/header-only フォーマット）、`rect_copied`、`vram_cleared`、`display_page_changed`、`page_size_changed`
- 表示ページのフィルタリング（`displayPage` フィールドで非表示ページのイベントを無視）
- `dirty` フラグによるVRAMからの直接リフレッシュ機能
- 詳細: `006-VRAMEnhancement.md` 3.7節, `012-VRAMPage.md` R5

#### R6: cmd/main.go のモジュール接続復元

- `--no-tcp` フラグの追加（TCPブリッジをオプショナル化）
- ChannelBus + TCPBridge 構成への移行（stash `tt-scaffold-checkpoint` に残存する変更を参照）
- `mon.SetVRAMAccessor(vramMod)` の呼び出し
- `OnClose` コールバックによるウィンドウ閉じ時のシャットダウン統合
- 既存の `--stats-port` フラグは維持する

#### R7: CPUデモプログラムの復元

`cpu.go` にシーン回転型のデモシステムを実装する。各シーンは一定時間（約3秒）で自動的に切り替わる。

| シーン | 名称 | 使用機能 | 参照仕様 |
|---|---|---|---|
| Scene 1 | Palette+Clear | `set_palette_block` + `clear_vram` でHSV全画面サイクリング | 006 REQ-11 |
| Scene 2 | BlitRect | `blit_rect` でチェッカーパターン描画＋色アニメーション | 006 REQ-11 |
| Scene 3 | Transform | `blit_rect_transform` でダイヤモンド回転（中央・左・右2倍拡大） | 006 REQ-11 |
| Scene 4 | AlphaBlend | `blit_rect` + 5種ブレンドモードでオーバーレイ表示 | 006 REQ-11 |
| Scene 5 | Scroll | `copy_rect` で横縞パターンの上スクロール | 006 REQ-11 |
| Scene 6 | PageComposite | ページ2枚でレイヤー合成（背景スクロール＋バウンシングダイヤモンド） | 012 R6 |
| Scene 7 | LargeMapScroll | ダブルバッファ＋512x512大マップ＋リサジュースクロール | 013 |

- 現在のパレットアニメーションデモは Scene 1 に統合する（既存動作との互換性を維持）
- ヘルパー関数群: `publishClearVRAM`, `publishBlitRect`, `publishBlitRectTransform`, `publishCopyRect`, `publishSetPageCount`, `publishSetDisplayPage`, `publishSetPageSize`, `publishCopyPage` 等
- ページ対応版ヘルパー: `publishClearVRAMPage`, `publishBlitRectPage` 等
- ticker は 8ms（~120FPS）で動作させる（既存の30FPSから変更）
- シャットダウン時に `sync.WaitGroup` で goroutine の完了を待機する

### 任意要件

- シーン遷移時のフェードイン/フェードアウト効果（ブレンドモード活用）
- シーン間で共通の初期化処理（`clear_vram` による画面クリア）

## 実現方針 (Implementation Approach)

### 全体方針

既存仕様書（006, 012, 013）の実装計画に基づき、以下の順序で段階的に再統合を行う。各段階でビルドとテストを実行し、リグレッションを確認する。

### 段階1: VRAM基本拡張（006相当）

```
vram.go の変更:
├── VRAMModule 構造体の拡張
│   ├── buffer → indexBuffer リネーム
│   ├── colorBuffer []uint8 追加（RGBA）
│   └── palette [256][4]uint8 に拡張（RGBA）
├── handleMessage の拡張
│   ├── clear_vram ハンドラ
│   ├── blit_rect ハンドラ（blend.go 統合）
│   ├── blit_rect_transform ハンドラ（transform.go 統合）
│   ├── read_rect ハンドラ
│   ├── copy_rect ハンドラ
│   ├── set_palette_block ハンドラ
│   └── read_palette_block ハンドラ
├── クリッピング関数の追加
└── イベント発行関数の追加
```

### 段階2: VRAMページ機能（012相当）

```
vram.go の変更:
├── pageBuffer 構造体の導入
│   ├── indexBuffer []uint8
│   └── colorBuffer []uint8
├── VRAMModule の pages []pageBuffer 移行
├── ページ管理コマンド追加
│   ├── set_page_count
│   ├── set_display_page
│   ├── swap_pages
│   └── copy_page
└── 全描画コマンドにページ引数追加
```

### 段階3: ビューポート機能（013相当）

```
vram.go の変更:
├── pageBuffer に width/height 追加
├── set_page_size コマンド追加
├── ViewportOffset() メソッド追加
└── ページ間操作のサイズ差異対応
```

### 段階4: Monitor拡張

```
monitor.go の変更:
├── VRAMAccessor インターフェース定義
├── 拡張イベントハンドラ追加
├── displayPage フィルタリング
└── dirty フラグ＋直接リフレッシュ
```

### 段階5: CPUデモ + main.go統合

```
cpu.go の変更:
├── シーンシステム（init/update パターン）
├── ヘルパー関数群
├── Scene 1〜7 の実装
└── ticker を 8ms に変更

cmd/main.go の変更:
├── ChannelBus + TCPBridge 構成
├── --no-tcp フラグ
├── VRAMAccessor 接続
└── OnClose シャットダウン統合
```

### スレッド安全性

- `handleMessage` 内で `mu.Lock()` を保持した状態で全描画操作を実行する
- `VRAMAccessor` による Monitor からの読み取りは `mu.RLock()` を使用する
- 既存の stats 計測（`get_stats` はロック取得前に処理）は維持する

## 検証シナリオ (Verification Scenarios)

### シナリオ1: VRAM基本拡張の動作確認

1. `clear_vram` でページ全体をパレットインデックス5で塗りつぶす
2. 全ピクセルがインデックス5であることを `read_rect` で確認する
3. `blit_rect` で 10x10 の矩形をインデックス3で書き込む
4. 書き込み結果を `read_rect` で確認する
5. `blit_rect_transform` で回転（90度）した矩形を書き込む
6. 結果が正しいか確認する
7. `copy_rect` で矩形をコピーし、コピー先のデータを確認する

### シナリオ2: ブレンド描画の動作確認

1. パレット設定: インデックス1 = (255,0,0,255), インデックス2 = (0,0,255,128)
2. `blit_rect` Replace モードでインデックス1の矩形を描画
3. `blit_rect` Alpha モードでインデックス2の矩形を上に重ねる
4. Monitor の `GetPixel` で結果を確認: R ≈ 127, G = 0, B ≈ 128

### シナリオ3: ページ機能の動作確認

1. `set_page_count(2)` でページを2つに設定する
2. ページ0にパターンA、ページ1にパターンBを描画する
3. `set_display_page(1)` で表示をページ1に切り替える
4. Monitor にパターンBが表示されることを確認する
5. `swap_pages(0, 1)` でスワップする
6. Monitor にパターンAが表示されることを確認する

### シナリオ4: ビューポートの動作確認

1. ページ0のサイズを 512x512 に設定する
2. 512x512 の大きなパターンを描画する
3. `set_viewport_offset` でオフセットを (100, 100) に設定する
4. Monitor に (100,100) からの 256x212 領域が表示されることを確認する

### シナリオ5: CPUデモの動作確認

1. 全7シーンが順次表示されること（各約3秒）
2. シーン間でハングやパニックが発生しないこと
3. シーン6（PageComposite）のページ機能使用後、他のシーンが正常にページ0で動作すること

### シナリオ6: パフォーマンス統計との共存

1. `--stats-port` フラグ付きで起動する
2. 各シーンのVRAMコマンド実行時間が stats に記録されること
3. HTTP エンドポイントから統計データが取得できること

## テスト項目 (Testing for the Requirements)

### 単体テスト

| テスト | 対象要件 | ファイル |
|---|---|---|
| `TestClearVRAM` | R2 (clear_vram) | `vram_test.go` |
| `TestBlitRect` | R2 (blit_rect) | `vram_test.go` |
| `TestBlitRectTransform` | R2 (blit_rect_transform) | `vram_test.go` |
| `TestReadRect` | R2 (read_rect) | `vram_test.go` |
| `TestCopyRect` | R2 (copy_rect) | `vram_test.go` |
| `TestCopyRectOverlap` | R2 (copy_rect 重複) | `vram_test.go` |
| `TestSetPaletteBlock` | R2 (set_palette_block) | `vram_test.go` |
| `TestReadPaletteBlock` | R2 (read_palette_block) | `vram_test.go` |
| `TestBlitRectAlphaBlend` | R2 (blit_rect + blend) | `vram_test.go` |
| `TestClipping` | R2 (境界クリッピング) | `vram_test.go` |
| `TestPageManagement` | R3 (set_page_count, set_display_page) | `vram_test.go` |
| `TestSwapPages` | R3 (swap_pages) | `vram_test.go` |
| `TestCopyPage` | R3 (copy_page) | `vram_test.go` |
| `TestPageSizeManagement` | R4 (set_page_size) | `vram_test.go` |
| `TestBlendReplace` / `TestBlendAlpha` / etc. | R2 (blend modes) | `blend_test.go` (既存) |
| `TestRotation90` / `TestScale2x` / etc. | R2 (transform) | `transform_test.go` (既存) |

### 統合テスト

| テスト | 対象要件 | ファイル |
|---|---|---|
| `TestBlitAndReadRect` | R2 (blit + read パイプライン) | `vram_enhancement_test.go` |
| `TestAlphaBlendPipeline` | R2 (ブレンド結果の視覚確認) | `vram_enhancement_test.go` |
| `TestDemoProgram` | R7 (デモの正常動作) | `vram_enhancement_test.go` |
| `TestPageManagementIntegration` | R3 (ページ操作) | `vram_page_test.go` |
| `TestPageDrawIsolationIntegration` | R3 (ページ間描画独立性) | `vram_page_test.go` |
| `TestPageDisplayIntegration` | R3 + R5 (表示ページ切替) | `vram_page_test.go` |
| `TestHTTPStatsIntegration` | R2 + 015 (stats共存) | `stats_http_test.go` (既存) |

### 検証コマンド

```bash
# 全体ビルドと単体テスト
scripts/process/build.sh

# 統合テスト
scripts/process/integration_test.sh

# 特定テストの実行
scripts/process/integration_test.sh --specify TestBlitAndReadRect
scripts/process/integration_test.sh --specify TestPageManagement
scripts/process/integration_test.sh --specify TestDemoProgram
```
