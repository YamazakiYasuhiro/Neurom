# app.Main シャットダウン時ハングアップ修正

## 1. 背景 (Background)

### 1.1. 課題

005-GracefulShutdown で導入したシャットダウン機構により、モニタウィンドウを閉じると各モジュール（CPU、VRAM、IO、Monitor の receiveLoop）のgoroutineはバス経由のシャットダウンメッセージを正しく受信して終了する。しかし、**プロセス自体が終了せずハングアップする**問題が発生している。

### 1.2. 原因分析

`golang.org/x/mobile/app.Main()` は、渡されたコールバック関数が return しても **関数自体は返らない** 仕様である。これは `golang.org/x/mobile` の既知の動作であり、プラットフォームのイベントループ管理に起因する。

現在のシャットダウンフローでは、`main.go` において `mon.RunMain()` の後に `cancel()` や `mgr.StopAll()` を配置しているが、`app.Main()` が返らないためこれらのコードに到達できない。

```
StageDead検出
  → publishShutdown() (1回目) → 各モジュールgoroutine終了
  → OnClose() → cancel()
  → break eventLoop
  → publishShutdown() (2回目、冗長)
  → コールバック return
  → app.Main() がブロック ← ★ ここで永久に停止
  → (以下到達不可)
  → mon.RunMain() return
  → cancel() / mgr.StopAll()
  → プロセス終了
```

### 1.3. 副次的な問題

`RunMain()` 内で `publishShutdown()` が2回呼ばれている:

1. **1回目**: `StageDead` 検出時（イベントループ内、`break eventLoop` の直前）
2. **2回目**: イベントループ終了後（ループの外）

これ自体はハングの直接原因ではないが、不要な二重送信である。

## 2. 要件 (Requirements)

### 2.1. プロセスの正常終了（必須）

- モニタウィンドウを閉じた際、プロセスがハングアップせず正常に終了すること。
- `app.Main()` が返らない場合でも、クリーンアップ処理（`mgr.StopAll()`、バスのクローズ、TCP ブリッジのクローズ、Stats サーバーの Shutdown 等）が確実に実行されること。
- プロセスは終了コード 0 で OS に制御を戻すこと。

### 2.2. クリーンアップの完全性（必須）

- 以下のリソースがシャットダウン時に確実に解放されること:
  - `mgr.StopAll()`: 全モジュールの `Stop()` 呼び出し（`wg.Wait()` 完了待ち）
  - `channelBus.Close()`: バスのクローズ
  - `tcpBridge.Close()`: TCP ブリッジのクローズ（有効な場合）
  - `ss.Shutdown()`: Stats サーバーのシャットダウン（有効な場合）

### 2.3. publishShutdown 二重呼び出しの解消（任意）

- `RunMain()` 内の `publishShutdown()` 呼び出しを1回に整理する。

### 2.4. Headless モードへの影響なし（必須）

- Headless モード（`--headless` フラグ）のシャットダウンフローに影響を与えないこと。

## 3. 実現方針 (Implementation Approach)

### 3.1. 方針: `OnClose` コールバックでのクリーンアップ + `os.Exit`

`app.Main()` が返らない以上、`mon.RunMain()` 以降のコードに依存するのではなく、`OnClose` コールバック内で全クリーンアップを完了させ、明示的に `os.Exit(0)` でプロセスを終了させる。

#### 3.1.1. main.go の変更

`OnClose` コールバックを以下のように変更する:

```go
mon := monitor.New(monitor.MonitorConfig{
    Headless: *headless,
    OnClose: func() {
        log.Println("[Main] OnClose: shutting down VM...")
        cancel()
        if err := mgr.StopAll(); err != nil {
            log.Printf("[Main] OnClose: errors during shutdown: %v", err)
        }
        if tcpBridge != nil {
            tcpBridge.Close()
        }
        channelBus.Close()
        log.Println("[Main] OnClose: shutdown complete. Exiting process.")
        os.Exit(0)
    },
})
```

**注意点**:
- `os.Exit(0)` は `defer` を実行しないため、クリーンアップ対象のリソースを `OnClose` 内で明示的にクローズする必要がある。
- `OnClose` は `StageDead` 検出時、`publishShutdown()` の直後に呼ばれる。この時点で `receiveLoop` を含む全モジュールの goroutine はバス経由で既にシャットダウンメッセージを受信して終了処理に入っている。そのため `mgr.StopAll()` → 各モジュールの `Stop()` → `wg.Wait()` は即座に完了する。
- Stats サーバーのシャットダウンも `OnClose` 内で処理する必要がある（`os.Exit()` は `defer` を実行しないため）。

#### 3.1.2. RunMain の publishShutdown 整理

イベントループ終了後（196行目）の `publishShutdown()` 呼び出しを削除し、`StageDead` 検出時（177行目）の1回のみにする。

### 3.2. 安全性の考慮

- `OnClose` は `app.Main` のコールバック内（イベントループのスレッド上）で呼ばれる。`mgr.StopAll()` がデッドロックしないことを確認する必要がある。
- 具体的には、`MonitorModule.Stop()` は `wg.Wait()` を呼ぶが、`receiveLoop` は `publishShutdown()` により既にシャットダウンメッセージを受信して終了しているため、`wg.Wait()` は即座に返る。

```
(イベントループスレッド)
  StageDead検出
  → publishShutdown()
    → bus.Publish("system", Shutdown)
    → receiveLoop がメッセージ受信 → return → wg.Done()
    → 他モジュールのgoroutineも終了
  → OnClose()
    → cancel()
    → mgr.StopAll()
      → mon.Stop() → wg.Wait() → 即座にreturn (receiveLoopは既に終了)
      → cpu.Stop() → wg.Wait() → 即座にreturn
      → vram.Stop() → wg.Wait() → 即座にreturn
      → io.Stop() → wg.Wait() → 即座にreturn
    → channelBus.Close()
    → tcpBridge.Close()
    → os.Exit(0) ← プロセス正常終了
```

## 4. 検証シナリオ (Verification Scenarios)

### シナリオ1: GUI モードでのウィンドウクローズ

1. エミュレータを GUI モード（`--headless` なし）で起動する。
2. モニタウィンドウが表示されることを確認する。
3. モニタウィンドウの「×」ボタンをクリックする。
4. 以下のログが出力されることを確認する:
   - `[Monitor] publishShutdown: shutdown message published successfully`（1回のみ）
   - `[Main] OnClose: shutting down VM...`
   - `[Main] OnClose: shutdown complete. Exiting process.`
5. プロセスがハングアップせず、終了コード 0 で終了すること。
6. `publishShutdown` のログが2回ではなく1回のみ出力されること。

### シナリオ2: Headless モードでの終了

1. エミュレータを `--headless` モードで起動する。
2. Ctrl+C（SIGINT）でプロセスを終了させる。
3. プロセスが従来通り正常に終了すること（回帰がないこと）。

### シナリオ3: Stats サーバー有効時の終了

1. エミュレータを `--stats-port 8088` で起動する。
2. モニタウィンドウの「×」ボタンをクリックする。
3. Stats サーバーを含む全リソースがクリーンアップされ、プロセスが正常に終了すること。

## 5. テスト項目 (Testing for the Requirements)

| 要件 | テストケース | 検証内容 | 検証コマンド |
|---|---|---|---|
| 2.1, 2.2 | `integration/shutdown_test.go` | Headless モードで起動した全モジュールがバス経由シャットダウンメッセージにより正常に終了すること（既存テストの回帰確認） | `scripts/process/integration_test.sh` |
| 2.4 | `integration/shutdown_test.go` | Headless モードのシャットダウンフローに回帰がないこと | `scripts/process/integration_test.sh` |
| 2.1 | 手動検証（シナリオ1） | GUI モードでウィンドウクローズ時にプロセスがハングアップせず正常終了すること | 手動実行: `./bin/neurom.exe` → ×ボタンクリック → プロセス終了を確認 |
| 2.3 | コードレビュー | `publishShutdown()` の呼び出しが `RunMain` 内で1回のみであること | `scripts/process/build.sh`（ビルド確認） |

**備考**: `app.Main()` を使用した GUI モードの終了は、`golang.org/x/mobile` のプラットフォーム依存動作により自動テストでの検証が困難であるため、シナリオ1は手動検証とする。自動テストでは Headless モードでの回帰がないことを確認する。
