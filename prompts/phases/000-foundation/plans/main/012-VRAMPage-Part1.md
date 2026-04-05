# 012-VRAMPage-Part1

> **Source Specification**: [prompts/phases/000-foundation/ideas/main/012-VRAMPage.md](file://prompts/phases/000-foundation/ideas/main/012-VRAMPage.md)

## Goal Description

VRAMモジュールに「ページ」の概念を導入する。Part1 では、データ構造の変更（R1）、ページ管理コマンド（R2: `set_page_count`、R3: `set_display_page`、R4: `swap_pages` / `copy_page`）を実装し、単体テストで検証する。

## User Review Required

None.

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| R1: ページデータ構造の導入 | Proposed Changes > vram/vram.go（pageBuffer構造体、VRAMModule変更） |
| R2: ページ数設定 (`set_page_count`) | Proposed Changes > vram/vram.go（handleSetPageCount） |
| R3: 表示ページ設定 (`set_display_page`) | Proposed Changes > vram/vram.go（handleSetDisplayPage） |
| R4-1: ページスワップ (`swap_pages`) | Proposed Changes > vram/vram.go（handleSwapPages） |
| R4-2: ページコピー (`copy_page`) | Proposed Changes > vram/vram.go（handleCopyPage） |
| R5: 全コマンドのページ引数化 | Part2 で実装 |
| R6: Monitorモジュールとの連携 | Part2 で実装 |

## Proposed Changes

### vram モジュール

#### [MODIFY] [features/neurom/internal/modules/vram/vram_test.go](file://features/neurom/internal/modules/vram/vram_test.go)

*   **Description**: R1〜R4 に対する単体テストを追加する（TDD: テストを先に記述）。
*   **Technical Design**:
    *   既存の `newTestVRAM()` / `drainBus()` ヘルパーをそのまま活用する。
    *   ページ管理に関するイベントを `testBus.ch` から受信して検証するパターン。

*   **Logic**:

    **テストケース一覧（テーブル駆動）**:

    ```go
    // TestPageInitialization: R1 の初期状態確認
    // - New() 直後、ページ数は1
    // - pages[0].index の長さが width*height
    // - pages[0].color の長さが width*height*4
    // - displayPage == 0

    // TestSetPageCount: R2 のテストテーブル
    tests := []struct {
        name          string
        count         int    // interpretedCount (1-256)
        rawCount      uint8  // wire format (0=256, 1-255=literal)
        expectPages   int
        expectEvent   string // "page_count_changed" or ""
        expectError   bool
    }{
        {"increase_to_4", 4, 4, 4, "page_count_changed", false},
        {"decrease_to_2", 2, 2, 2, "page_count_changed", false},
        {"same_value_idempotent", 2, 2, 2, "page_count_changed", false},
        {"set_to_256", 256, 0, 256, "page_count_changed", false},
        {"back_to_1", 1, 1, 1, "page_count_changed", false},
    }
    // 各ケースで:
    //   handleMessage(set_page_count, Data=[rawCount])
    //   len(v.pages) == expectPages を検証
    //   イベント Target == expectEvent を検証
    //   増加時: 新規ページが全ゼロクリアか検証
    //   減少時: displayPage >= count なら 0 にリセットされるか検証

    // TestSetPageCountNewPagesZeroCleared: R2 増加時の初期化
    // - set_page_count(3)
    // - pages[1], pages[2] の index / color が全て 0 であること

    // TestSetPageCountDisplayPageReset: R2 減少時の displayPage リセット
    // - set_page_count(4)
    // - set_display_page(3)
    // - drainBus(tb)
    // - set_page_count(2)
    // - displayPage == 0 であること
    // - display_page_changed イベント (page=0) が発行されていること
    // - page_count_changed イベント (count=2) が発行されていること

    // TestSetDisplayPage: R3 のテスト
    tests := []struct {
        name        string
        pageCount   int
        displayPage uint8
        expectError bool
    }{
        {"valid_page", 4, 2, false},
        {"page_0", 4, 0, false},
        {"invalid_page", 2, 5, true},
    }
    // 各ケースで:
    //   set_page_count → set_display_page
    //   正常: displayPage が更新、display_page_changed イベント
    //   異常: displayPage 変更なし、page_error イベント

    // TestSwapPages: R4-1 のテスト
    // - set_page_count(2)
    // - pages[0].index を pattern A で埋める
    // - pages[1].index を pattern B で埋める
    // - swap_pages(0, 1)
    // - pages[0] が pattern B、pages[1] が pattern A であること
    // - pages_swapped イベント
    // - displayPage は変更なし

    // TestSwapPagesInvalidPage: R4-1 エラーケース
    // - swap_pages(0, 5) → page_error

    // TestCopyPage: R4-2 のテスト
    // - set_page_count(2)
    // - pages[0].index を pattern で埋める
    // - copy_page(0, 1)
    // - pages[1] が pages[0] と同内容であること
    // - pages[0] は変更なし
    // - page_copied イベント

    // TestCopyPageSamePage: R4-2 冪等性
    // - copy_page(0, 0) → 何も変わらない、page_copied イベント

    // TestCopyPageInvalidPage: R4-2 エラーケース
    // - copy_page(0, 5) → page_error
    ```

#### [MODIFY] [features/neurom/internal/modules/vram/vram.go](file://features/neurom/internal/modules/vram/vram.go)

*   **Description**: ページデータ構造の導入（R1）と、ページ管理コマンド群（R2〜R4）のハンドラを追加する。
*   **Technical Design**:

    **データ構造の変更:**

    ```go
    // pageBuffer holds per-page pixel data.
    type pageBuffer struct {
        index []uint8 // palette index buffer (width * height)
        color []uint8 // RGBA color buffer (width * height * 4)
    }

    // newPageBuffer creates a zero-initialized page buffer.
    func newPageBuffer(w, h int) pageBuffer {
        return pageBuffer{
            index: make([]uint8, w*h),
            color: make([]uint8, w*h*4),
        }
    }

    type VRAMModule struct {
        mu          sync.RWMutex
        width       int
        height      int
        color       int
        pages       []pageBuffer  // pages[0] is the initial page
        displayPage int           // page shown by Monitor (R3)
        palette     [256][4]uint8
        bus         bus.Bus
        wg          sync.WaitGroup
    }
    ```

    **既存フィールド `buffer` / `colorBuffer` の除去:**
    - `buffer` → `pages[0].index` に置き換え
    - `colorBuffer` → `pages[0].color` に置き換え
    - Part1 では暫定的に `pages[0]` をハードコードで参照するヘルパーを用意し、Part2 でページ引数対応に切り替える

    **New() の変更:**

    ```go
    func New() *VRAMModule {
        w, h := 256, 212
        return &VRAMModule{
            width:  w,
            height: h,
            color:  8,
            pages:  []pageBuffer{newPageBuffer(w, h)},
            // displayPage: 0 (zero value)
        }
    }
    ```

    **VRAMAccessor メソッドの変更（R6 に向けた暫定対応）:**

    ```go
    func (v *VRAMModule) VRAMBuffer() []uint8      { return v.pages[v.displayPage].index }
    func (v *VRAMModule) VRAMColorBuffer() []uint8  { return v.pages[v.displayPage].color }
    // VRAMWidth, VRAMHeight, VRAMPalette, RLock, RUnlock は変更なし
    ```

    **ページ番号バリデーションヘルパー:**

    ```go
    // interpretPageCount converts wire-format count (u8) to actual count (1-256).
    // 0 is interpreted as 256.
    func interpretPageCount(raw uint8) int {
        if raw == 0 {
            return 256
        }
        return int(raw)
    }

    // encodePageCount converts actual count (1-256) to wire-format (u8).
    func encodePageCount(count int) uint8 {
        if count == 256 {
            return 0
        }
        return uint8(count)
    }

    // isValidPage checks if the given page index is within the current page range.
    func (v *VRAMModule) isValidPage(page int) bool {
        return page >= 0 && page < len(v.pages)
    }

    // publishPageError publishes a page_error event with the given error code.
    // Error codes: 0x03 = invalid page number
    func (v *VRAMModule) publishPageError(code uint8) {
        v.bus.Publish("vram_update", &bus.BusMessage{
            Target:    "page_error",
            Operation: bus.OpCommand,
            Data:      []byte{code},
            Source:    v.Name(),
        })
    }
    ```

*   **Logic**:

    **handleMessage への新 Target 追加:**

    ```go
    // handleMessage の switch に以下を追加:
    case "set_page_count":
        v.handleSetPageCount(msg.Data)
    case "set_display_page":
        v.handleSetDisplayPage(msg.Data)
    case "swap_pages":
        v.handleSwapPages(msg.Data)
    case "copy_page":
        v.handleCopyPage(msg.Data)
    ```

    **handleSetPageCount (R2):**

    ```go
    func (v *VRAMModule) handleSetPageCount(data []byte) {
        // data: [count:u8]
        // count=0 means 256, count=1-255 is literal
        if len(data) < 1 { return }

        newCount := interpretPageCount(data[0])
        currentCount := len(v.pages)

        if newCount == currentCount {
            // Idempotent: publish event but no change
            // publish page_count_changed with encodePageCount(newCount)
            return
        }

        if newCount > currentCount {
            // Grow: append zero-initialized pages
            for i := currentCount; i < newCount; i++ {
                v.pages = append(v.pages, newPageBuffer(v.width, v.height))
            }
        } else {
            // Shrink: truncate pages slice
            v.pages = v.pages[:newCount]
            // Reset displayPage if out of range
            if v.displayPage >= newCount {
                v.displayPage = 0
                // Notify Monitor (bus mode) about the displayPage reset
                v.bus.Publish("vram_update", &bus.BusMessage{
                    Target:    "display_page_changed",
                    Operation: bus.OpCommand,
                    Data:      []byte{0},
                    Source:    v.Name(),
                })
            }
        }

        // publish page_count_changed event
        v.bus.Publish("vram_update", &bus.BusMessage{
            Target:    "page_count_changed",
            Operation: bus.OpCommand,
            Data:      []byte{encodePageCount(newCount)},
            Source:    v.Name(),
        })
    }
    ```

    **handleSetDisplayPage (R3):**

    ```go
    func (v *VRAMModule) handleSetDisplayPage(data []byte) {
        // data: [page:u8]
        if len(data) < 1 { return }

        page := int(data[0])
        if !v.isValidPage(page) {
            v.publishPageError(0x03) // invalid page number
            return
        }

        v.displayPage = page

        v.bus.Publish("vram_update", &bus.BusMessage{
            Target:    "display_page_changed",
            Operation: bus.OpCommand,
            Data:      []byte{data[0]},
            Source:    v.Name(),
        })
    }
    ```

    **handleSwapPages (R4-1):**

    ```go
    func (v *VRAMModule) handleSwapPages(data []byte) {
        // data: [page1:u8][page2:u8]
        if len(data) < 2 { return }

        p1, p2 := int(data[0]), int(data[1])
        if !v.isValidPage(p1) || !v.isValidPage(p2) {
            v.publishPageError(0x03)
            return
        }

        // Swap: Go's multiple assignment swaps the pageBuffer structs
        // (slice headers are swapped, underlying arrays stay in place)
        v.pages[p1], v.pages[p2] = v.pages[p2], v.pages[p1]

        v.bus.Publish("vram_update", &bus.BusMessage{
            Target:    "pages_swapped",
            Operation: bus.OpCommand,
            Data:      []byte{data[0], data[1]},
            Source:    v.Name(),
        })
    }
    ```

    **handleCopyPage (R4-2):**

    ```go
    func (v *VRAMModule) handleCopyPage(data []byte) {
        // data: [src_page:u8][dst_page:u8]
        if len(data) < 2 { return }

        src, dst := int(data[0]), int(data[1])
        if !v.isValidPage(src) || !v.isValidPage(dst) {
            v.publishPageError(0x03)
            return
        }

        if src != dst {
            copy(v.pages[dst].index, v.pages[src].index)
            copy(v.pages[dst].color, v.pages[src].color)
        }

        v.bus.Publish("vram_update", &bus.BusMessage{
            Target:    "page_copied",
            Operation: bus.OpCommand,
            Data:      []byte{data[0], data[1]},
            Source:    v.Name(),
        })
    }
    ```

    **既存コマンドの暫定対応（Part1 の互換性維持）:**

    Part1 の時点では、既存コマンド（`draw_pixel`, `blit_rect` 等）はまだページ引数を持たない。`buffer` / `colorBuffer` への直接参照を `pages[0].index` / `pages[0].color` に置き換えるだけとする。Part2 でページ引数のパース処理を追加する。

    具体的には、`handleMessage` 内および各ハンドラ内の:
    - `v.buffer` → `v.pages[0].index`
    - `v.colorBuffer` → `v.pages[0].color`

    に一括置換する。`mode` ハンドラでは全ページを再初期化する:

    ```go
    case "mode":
        for i := range v.pages {
            v.pages[i] = newPageBuffer(v.width, v.height)
        }
        // publish mode_changed event (既存と同じ)
    ```

## Step-by-Step Implementation Guide

1.  [x] **単体テストの作成（TDD）**:
    *   Edit `features/neurom/internal/modules/vram/vram_test.go`
    *   `TestPageInitialization`, `TestSetPageCount`, `TestSetPageCountNewPagesZeroCleared`, `TestSetPageCountDisplayPageReset`, `TestSetDisplayPage`, `TestSwapPages`, `TestSwapPagesInvalidPage`, `TestCopyPage`, `TestCopyPageSamePage`, `TestCopyPageInvalidPage` を追加

2.  [x] **pageBuffer 構造体と newPageBuffer の追加**:
    *   Edit `features/neurom/internal/modules/vram/vram.go`
    *   `pageBuffer` 構造体と `newPageBuffer()` 関数を追加
    *   `interpretPageCount`, `encodePageCount`, `isValidPage`, `publishPageError` ヘルパーを追加

3.  [x] **VRAMModule のフィールド変更**:
    *   Edit `features/neurom/internal/modules/vram/vram.go`
    *   `buffer []uint8` → 削除、`colorBuffer []uint8` → 削除
    *   `pages []pageBuffer` と `displayPage int` を追加
    *   `New()` を修正: `pages: []pageBuffer{newPageBuffer(w, h)}`

4.  [x] **既存コマンドの buffer/colorBuffer 参照を pages[0] に置換**:
    *   Edit `features/neurom/internal/modules/vram/vram.go`
    *   全ハンドラ内の `v.buffer` → `v.pages[0].index`、`v.colorBuffer` → `v.pages[0].color` に一括置換
    *   `VRAMBuffer()` → `v.pages[v.displayPage].index`、`VRAMColorBuffer()` → `v.pages[v.displayPage].color`
    *   `mode` ハンドラ: 全ページを `newPageBuffer` で再初期化

5.  [x] **ページ管理コマンドハンドラの追加**:
    *   Edit `features/neurom/internal/modules/vram/vram.go`
    *   `handleMessage` の switch に `set_page_count`, `set_display_page`, `swap_pages`, `copy_page` を追加
    *   各ハンドラ関数を実装

6.  [x] **ビルドと単体テスト**:
    *   `./scripts/process/build.sh` を実行
    *   全テストがパスすることを確認

## Verification Plan

### Automated Verification

1.  **Build & Unit Tests**:
    ```bash
    ./scripts/process/build.sh
    ```

## Documentation

#### [MODIFY] [prompts/phases/000-foundation/ideas/main/012-VRAMPage.md](file://prompts/phases/000-foundation/ideas/main/012-VRAMPage.md)
*   **更新内容**: 実装進捗に応じてステータスを更新（必要に応じて）

## 継続計画について

Part2（`012-VRAMPage-Part2.md`）で以下を実装する:
- R5: 全コマンドのページ引数化（既存コマンドのバイナリフォーマット変更）
- R6: Monitorモジュールとの連携
- CPUモジュールのデモコード更新
- 既存統合テストの更新と新規統合テストの追加
