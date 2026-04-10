# 023-ZMQBusPanicElimination

> **Source Specification**: [023-ZMQBusPanicElimination.md](file://prompts/phases/000-foundation/ideas/main/023-ZMQBusPanicElimination.md)

## Goal Description

統合テストにおいて `go-zeromq/zmq4` v0.17.0 の inproc トランスポートで断続的に発生するパニック (`makeslice: len out of range`) を根本的に排除する。残存する2つの統合テスト (`TestVRAMMonitorIntegration`, `TestPaletteUpdate`) で使用している `bus.NewZMQBus()` を `bus.NewChannelBus()` に置き換える。

## User Review Required

None.

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| R1: ZMQBus 直接利用の排除 | Proposed Changes > `vram_monitor_test.go`, `palette_test.go` |
| R2: テストの機能的正当性の維持 | Proposed Changes > 各ファイルのアサーション維持 |
| R3: パニックフリーの確認 | Verification Plan > `-count=5` 繰り返し実行 |
| R4: ZMQBus 単体テスト存続確認 | Verification Plan > bus パッケージ単体テスト |

## Proposed Changes

### 統合テスト (integration)

#### [MODIFY] [vram_monitor_test.go](file://features/neurom/integration/vram_monitor_test.go)

*   **Description**: `NewZMQBus` を `NewChannelBus` に変更し、ZMQ 依存を排除する
*   **Technical Design**:
    *   関数 `TestVRAMMonitorIntegration` の先頭でバスを生成する処理を変更
    *   `NewZMQBus` は `(*ZMQBus, error)` を返すが、`NewChannelBus` は `*ChannelBus` のみを返す（エラーなし）
    *   `Subscribe` の `error` 戻り値は `ChannelBus` でも返すため（インターフェース準拠）、そちらのエラーハンドリングはそのまま残す
*   **Logic (Before → After)**:

    Before (L15-20):
    ```go
    b, err := bus.NewZMQBus("inproc://test-integration-bus")
    if err != nil {
        t.Fatalf("Failed to init bus: %v", err)
    }
    defer b.Close()
    ```

    After:
    ```go
    b := bus.NewChannelBus()
    defer b.Close()
    ```

    *   L22 `module.NewManager(b)`: 変更不要。`NewManager` は `bus.Bus` インターフェースを受け取り、`*ChannelBus` は `Bus` インターフェースを実装済み。
    *   L30-33 `b.Subscribe("vram_update")`: 変更不要。`ChannelBus.Subscribe` も `(<-chan *BusMessage, error)` を返す。
    *   L44-62 のアサーションロジック: 変更不要。
    *   L64-67 のシャットダウンロジック: 変更不要。

---

#### [MODIFY] [palette_test.go](file://features/neurom/integration/palette_test.go)

*   **Description**: `NewZMQBus` を `NewChannelBus` に変更し、ZMQ 依存を排除する
*   **Technical Design**:
    *   関数 `TestPaletteUpdate` の先頭でバスを生成する処理を変更
    *   `NewZMQBus` → `NewChannelBus` への置換パターンは `vram_monitor_test.go` と同一
*   **Logic (Before → After)**:

    Before (L14-19):
    ```go
    b, err := bus.NewZMQBus("inproc://test-palette-bus")
    if err != nil {
        t.Fatalf("Failed to init bus: %v", err)
    }
    defer b.Close()
    ```

    After:
    ```go
    b := bus.NewChannelBus()
    defer b.Close()
    ```

    *   L27 `module.NewManager(b)`: 変更不要。同上。
    *   L35 `time.Sleep(100 * time.Millisecond)`: `ChannelBus` では Subscribe が即時反映されるため理論上不要だが、モジュール起動の安定化のために**残す**。
    *   L38-44 `b.Publish(...)`: `ChannelBus.Publish` も `error` を返すため、エラーハンドリングは**そのまま維持**する。
    *   L49-54 `b.Publish(...)`: 同上。
    *   L60-63 のアサーション (`m.GetPixel`): 変更不要。

## Step-by-Step Implementation Guide

- [x] **Step 1: `vram_monitor_test.go` の修正**
    *   `features/neurom/integration/vram_monitor_test.go` を編集
    *   L16-19 の `NewZMQBus` 呼び出し + エラーハンドリングを `NewChannelBus()` に置換
    *   具体的には以下の4行:
        ```go
        b, err := bus.NewZMQBus("inproc://test-integration-bus")
        if err != nil {
            t.Fatalf("Failed to init bus: %v", err)
        }
        ```
        を以下の1行に置換:
        ```go
        b := bus.NewChannelBus()
        ```

- [x] **Step 2: `palette_test.go` の修正**
    *   `features/neurom/integration/palette_test.go` を編集
    *   L15-18 の `NewZMQBus` 呼び出し + エラーハンドリングを `NewChannelBus()` に置換
    *   具体的には以下の4行:
        ```go
        b, err := bus.NewZMQBus("inproc://test-palette-bus")
        if err != nil {
            t.Fatalf("Failed to init bus: %v", err)
        }
        ```
        を以下の1行に置換:
        ```go
        b := bus.NewChannelBus()
        ```

- [x] **Step 3: ビルドと単体テスト**
    *   `build.sh` を実行してビルドエラーと単体テスト失敗がないことを確認

- [x] **Step 4: 統合テスト実行 (単発)**
    *   統合テストを `-count=1` で実行し全テスト PASS を確認

- [x] **Step 5: パニックフリー確認 (繰り返し)**
    *   統合テストを `-count=5` で実行しパニックが再発しないことを確認

- [x] **Step 6: ZMQ 残存確認**
    *   `integration/` ディレクトリ内で `NewZMQBus` が0件であることを `grep` で確認

## Verification Plan

### Automated Verification

1.  **Build & Unit Tests**:
    ```bash
    ./scripts/process/build.sh
    ```

2.  **Integration Tests (単発)**:
    ```bash
    ./scripts/process/integration_test.sh
    ```
    *   **Log Verification**: 全テストが `PASS` であること。`panic` が出力に含まれないこと。

3.  **Integration Tests (繰り返し — パニックフリー確認)**:
    ```bash
    ./scripts/process/integration_test.sh --specify "TestVRAMMonitorIntegration|TestPaletteUpdate"
    ```
    上記テストを 5 回繰り返し実行して安定性を確認する。

4.  **ZMQ 残存確認**:
    統合テストディレクトリ内で `NewZMQBus` が使用されていないことを確認する。

## Documentation

本修正は統合テストのバス実装を切り替えるのみであり、既存の仕様書やドキュメントへの影響はない。

#### [MODIFY] [023-ZMQBusPanicElimination.md](file://prompts/phases/000-foundation/ideas/main/023-ZMQBusPanicElimination.md)
*   **更新内容**: 実装完了後、仕様書末尾に「対応完了」ステータスを追記
