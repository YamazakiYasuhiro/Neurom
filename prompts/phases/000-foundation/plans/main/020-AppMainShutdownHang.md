# 020-AppMainShutdownHang

> **Source Specification**: prompts/phases/000-foundation/ideas/main/020-AppMainShutdownHang.md

## Goal Description
`golang.org/x/mobile/app.Main()` がコールバック終了後も返らない仕様に起因するプロセスハングアップを修正する。`OnClose` コールバック内で全クリーンアップを完了し、`os.Exit(0)` で明示的にプロセスを終了させる。副次的に `publishShutdown()` の二重呼び出しを解消する。

## User Review Required
None.

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| 2.1 プロセスの正常終了 | Proposed Changes > `main.go` — `OnClose` コールバックで `os.Exit(0)` |
| 2.2 クリーンアップの完全性 | Proposed Changes > `main.go` — `OnClose` 内で `mgr.StopAll()`, `channelBus.Close()`, `tcpBridge.Close()`, `ss.Shutdown()` を明示実行 |
| 2.3 publishShutdown 二重呼び出し解消 | Proposed Changes > `monitor.go` — イベントループ後の `publishShutdown()` 削除 |
| 2.4 Headless モードへの影響なし | Proposed Changes > `main.go` — Headless パスは既存のまま維持 |

## Proposed Changes

### monitor

#### [MODIFY] [features/neurom/internal/modules/monitor/monitor.go](file://features/neurom/internal/modules/monitor/monitor.go)
*   **Description**: `RunMain()` 内の `publishShutdown()` 二重呼び出しを解消する。イベントループ終了後（ループ外）の呼び出しを削除し、`StageDead` 検出時（ループ内）の1回のみに整理する。
*   **Technical Design**:
    *   現在の `RunMain()` 末尾のコールバック部分:
        ```go
        // 現在:
        log.Println("[Monitor] RunMain: event loop exited")
        m.publishShutdown()  // ← この呼び出しを削除
        log.Println("[Monitor] RunMain: app.Main callback returning")
        ```
    *   修正後:
        ```go
        log.Println("[Monitor] RunMain: event loop exited")
        log.Println("[Monitor] RunMain: app.Main callback returning")
        ```
*   **Logic**:
    *   `StageDead` 検出時（177行目）に `publishShutdown()` → `OnClose()` → `break eventLoop` の順で処理される。
    *   イベントループ外（196行目）の `publishShutdown()` は、既にモジュールが終了済みのため不要。削除する。

### cmd

#### [MODIFY] [features/neurom/cmd/main.go](file://features/neurom/cmd/main.go)
*   **Description**: `OnClose` コールバックで全リソースのクリーンアップと `os.Exit(0)` を実行する。`os.Exit()` は `defer` を実行しないため、`defer` に依存していたリソース解放を `OnClose` 内で明示的に行う。
*   **Technical Design**:
    *   **Stats サーバー変数のスコープ拡張**:
        *   現在 `ss` は `if *statsPort != ""` ブロック内のローカル変数。`OnClose` クロージャからアクセスするため、関数スコープに昇格させる。
        ```go
        var ss *statsserver.StatsServer
        ```
    *   **`OnClose` コールバックの変更**:
        *   現在: `OnClose: cancel` （context の cancel 関数のみ）
        *   変更後: 全クリーンアップを行うクロージャ
        ```go
        mon := monitor.New(monitor.MonitorConfig{
            Headless: *headless,
            OnClose: func() {
                log.Println("[Main] OnClose: shutting down VM...")
                cancel()
                // mgr.StopAll() — 全モジュールの Stop() を呼び wg.Wait()
                if err := mgr.StopAll(); err != nil {
                    log.Printf("[Main] OnClose: errors during shutdown: %v", err)
                }
                // Stats サーバーのシャットダウン (有効な場合)
                if ss != nil {
                    shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
                    defer shutCancel()
                    ss.Shutdown(shutCtx)
                }
                // TCP ブリッジのクローズ (有効な場合)
                if tcpBridge != nil {
                    tcpBridge.Close()
                }
                // バスのクローズ
                channelBus.Close()
                log.Println("[Main] OnClose: shutdown complete. Exiting process.")
                os.Exit(0)
            },
        })
        ```
    *   **Stats サーバー初期化の変更**:
        *   `ss :=` を `ss =` に変更（スコープ昇格に伴う代入演算子の変更）。
        ```go
        if *statsPort != "" {
            ss = statsserver.New(vramMod, mon, *statsPort)
            // ...（defer は維持 — Headless モードの正常終了パスで使用）
        }
        ```
*   **Logic**:
    *   GUI モードでは `app.Main()` が返らないため、`OnClose` 内で以下の順序でクリーンアップを実行する:
        1. `cancel()` — context をキャンセル（goroutine の `ctx.Done()` を発火）
        2. `mgr.StopAll()` — 全モジュールの `Stop()` → `wg.Wait()`。`publishShutdown()` でシャットダウンメッセージ送信済みのため、各モジュールの goroutine は既に終了しており即座に完了する。
        3. `ss.Shutdown()` — Stats サーバーの HTTP リスナーを停止。
        4. `tcpBridge.Close()` — TCP ブリッジのクローズ。
        5. `channelBus.Close()` — バスのクローズ。
        6. `os.Exit(0)` — プロセスを明示的に終了。
    *   Headless モードでは `OnClose` は呼ばれない（`RunMain()` が即 return）。シャットダウンは既存の `select` → `cancel()` → `mgr.StopAll()` パスで処理される。`defer` によるリソース解放も正常に動作する。

### デッドロック安全性の確認

`OnClose` は `StageDead` 検出時に `publishShutdown()` の直後に呼ばれる。シャットダウンメッセージの伝播順序は以下の通り:

```
publishShutdown()
  → bus.Publish("system", Shutdown)
    → ChannelBus.Publish はチャネルに同期送信 (select + default)
    → 各 receiveLoop がチャネルからメッセージを受信
      → Monitor.receiveLoop: return → wg.Done()
      → CPU.run: return → wg.Done()
      → VRAM.run: return → wg.Done()
      → IO.run: return → wg.Done()
  → publishShutdown() return

OnClose() ← ここで呼ばれる
  → mgr.StopAll()
    → mon.Stop() → wg.Wait() → 即座に return
    → cpu.Stop() → wg.Wait() → 即座に return
    → vram.Stop() → wg.Wait() → 即座に return
    → io.Stop() → wg.Wait() → 即座に return
```

`ChannelBus.Publish()` はバッファ付きチャネル（容量100）へ非ブロッキング送信（`select + default`）するため、`publishShutdown()` はサブスクライバーの処理を待たずに即座に返る。しかし、`publishShutdown()` が返った直後に `OnClose()` → `mgr.StopAll()` が呼ばれるため、goroutine がメッセージを処理して終了するまでに若干の猶予が必要。

実際には:
- チャネル送信は即座にバッファに格納される。
- 各 goroutine のメッセージ処理 → return → `wg.Done()` は非常に高速（マイクロ秒オーダー）。
- `OnClose()` から `mgr.StopAll()` に至るまでにも `cancel()` の呼び出し等で僅かな時間が経過する。
- 万一 `wg.Wait()` がブロックしても、goroutine が即座にメッセージを処理して終了するため、事実上のデッドロックは発生しない。

## Step-by-Step Implementation Guide

1.  **[ ] Step 1: monitor.go — publishShutdown 二重呼び出し解消**:
    *   Edit `features/neurom/internal/modules/monitor/monitor.go` の `RunMain()` メソッドから、イベントループ後（196行目）の `m.publishShutdown()` 呼び出しを削除する。

2.  **[ ] Step 2: main.go — Stats サーバー変数のスコープ昇格**:
    *   Edit `features/neurom/cmd/main.go` にて、Stats サーバー初期化ブロックの前に `var ss *statsserver.StatsServer` を追加する。
    *   `if *statsPort != ""` ブロック内の `ss := statsserver.New(...)` を `ss = statsserver.New(...)` に変更する。

3.  **[ ] Step 3: main.go — OnClose コールバックの変更**:
    *   `monitor.MonitorConfig` の `OnClose` フィールドを、`cancel` 単体から全クリーンアップを行うクロージャに変更する。
    *   クロージャ内で `cancel()` → `mgr.StopAll()` → `ss.Shutdown()` → `tcpBridge.Close()` → `channelBus.Close()` → `os.Exit(0)` の順にリソース解放を行う。

4.  **[ ] Step 4: ビルド確認**:
    *   `scripts/process/build.sh` を実行してビルドエラーがないことを確認する。

5.  **[ ] Step 5: 既存統合テストの回帰確認**:
    *   `scripts/process/integration_test.sh --specify "TestGracefulShutdownViaBus"` を実行して Headless モードのシャットダウンに回帰がないことを確認する。

6.  **[ ] Step 6: 手動検証（GUI モード）**:
    *   `./bin/neurom.exe` を起動し、ウィンドウの ×ボタンをクリックしてプロセスが正常終了することを確認する。
    *   ログ出力に以下が含まれることを確認:
        *   `[Monitor] publishShutdown: shutdown message published successfully` が **1回のみ**
        *   `[Main] OnClose: shutting down VM...`
        *   `[Main] OnClose: shutdown complete. Exiting process.`

## Verification Plan

### Automated Verification

1.  **Build & Unit Tests**:
    run the build script.
    ```bash
    ./scripts/process/build.sh
    ```

2.  **Integration Tests**:
    Run integration tests.
    ```bash
    ./scripts/process/integration_test.sh --specify "TestGracefulShutdownViaBus"
    ```
    *   **Log Verification**: テストが5秒以内に完了し、"StopAll timed out" が発生しないこと。

### Manual Verification

1.  **GUI モード終了確認**:
    ```bash
    ./bin/neurom.exe
    ```
    *   ×ボタンクリック後、プロセスがハングせず終了すること。
    *   `publishShutdown` ログが1回のみ出力されること。
    *   `[Main] OnClose: shutdown complete. Exiting process.` が出力されること。

2.  **Stats サーバー有効時の終了確認**:
    ```bash
    ./bin/neurom.exe -stats-port 8088
    ```
    *   ×ボタンクリック後、Stats サーバーを含む全リソースが解放されプロセスが終了すること。

## Documentation

本修正は既存仕様 005-GracefulShutdown の補完であるため、仕様書を更新する。

#### [MODIFY] [prompts/phases/000-foundation/ideas/main/005-GracefulShutdown.md](file://prompts/phases/000-foundation/ideas/main/005-GracefulShutdown.md)
*   **更新内容**: セクション 3.4 に `app.Main()` が返らない問題への対処（`OnClose` での `os.Exit(0)`）に関する注記を追加する。020-AppMainShutdownHang で修正された旨を記載する。
