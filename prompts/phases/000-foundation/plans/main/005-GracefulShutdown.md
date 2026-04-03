# 005-GracefulShutdown

> **Source Specification**: prompts/phases/000-foundation/ideas/main/005-GracefulShutdown.md

## Goal Description
すべてのモジュールに対するグレースフルシャットダウン（安全な終了）機能を追加します。ZeroMQのバス経由で「System Shutdown」メッセージを受け取った際に、各モジュールが内部のイベントループ（Goroutine）を安全に終了するようにし、さらにMonitorウィンドウの終了イベント（×ボタン押下時）にも連動してそのシャットダウンメッセージをシステムバスへパブリッシュする仕組みを整備します。

## User Review Required
None.

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| 2.1 各モジュールのグレースフルシャットダウン | Proposed Changes > `cpu.go`, `vram.go`, `monitor.go`, `io.go` |
| 2.2 Monitorウィンドウからの終了メッセージ送信 | Proposed Changes > `monitor.go` |
| 2.3 システム終了制御の共通化 | Proposed Changes > `message.go` |
| 4. 検証シナリオ | Verification Plan > Integration Tests |

## Proposed Changes

### bus

#### [MODIFY] features/neurom/internal/bus/message.go
*   **Description**: システムコマンド用の定数を追加します。
*   **Technical Design**:
    *   ```go
        const (
            TargetSystem = "System"
            CmdShutdown  = "Shutdown"
        )
        ```
*   **Logic**:
    *   システム全体に関わるブロードキャスト用途のターゲットと、シャットダウンを意味するデータ本体の定数定義を追加します。

### modules

#### [MODIFY] features/neurom/internal/modules/cpu/cpu.go
*   **Description**: グレースフルシャットダウンのための `sync.WaitGroup` 管理基盤と、`system` トピックへのサブスクライブを追加します。
*   **Technical Design**:
    *   ```go
        type CPUModule struct {
            wg sync.WaitGroup
        }
        func (c *CPUModule) Start(ctx context.Context, b bus.Bus) error
        func (c *CPUModule) Stop() error
        func (c *CPUModule) run(ctx context.Context, b bus.Bus, sysCh <-chan *bus.BusMessage)
        ```
*   **Logic**:
    *   `Start` 時に `b.Subscribe("system")` で `sysCh` を取得し、`c.wg.Add(1)` としてから `go func()` により `run` を起動、`defer c.wg.Done()` します。
    *   `Stop` 時には `c.wg.Wait()` を実行し、Goroutineの終了をブロックして待ち合わせます。
    *   `run` 内部のループ処理内の `select` に `case msg := <-sysCh:` を追加。「`msg.Target == bus.TargetSystem && string(msg.Data) == bus.CmdShutdown`」であれば即座に `return` を呼び出して終了させます。

#### [MODIFY] features/neurom/internal/modules/vram/vram.go
*   **Description**: CPUと同等のグレースフルシャットダウン機能を追加します。
*   **Technical Design**:
    *   `wg sync.WaitGroup` を用い、`Start` で `sysCh` をサブスクライブし `go v.run(...)` 等を待ち合わせます。
    *   `Stop` にて `v.wg.Wait()` とします。
*   **Logic**:
    *   `run` ループ内で `sysCh` からのシャットダウンコマンド監視を行い、同様にイベントループを `return` します。

#### [MODIFY] features/neurom/internal/modules/io/io.go
*   **Description**: IOモジュール用の終了待ち合わせ機構を追加します。
*   **Technical Design**:
    *   これまでのファイルと同様の `wg sync.WaitGroup` パターンを実装します。
*   **Logic**:
    *   単純な `<-ctx.Done()` 待ちのGoroutineを、`<-sysCh` からのシャットダウンコマンドも `select` に追加して待つ方式に書き換えます。

#### [MODIFY] features/neurom/internal/modules/monitor/monitor.go
*   **Description**: Monitorモジュールの閉じるイベントハンドリングと、システムバスからの終了ハンドリングを追加します。
*   **Technical Design**:
    *   `Start` にて `sysCh` を取得して `wg` パターンを挿入。`receiveLoop` に `sysCh` の待ち受けを追加。
    *   `RunMain` による UI ウィンドウ制御層にて、システム終了時の Publish 処理を追加。
*   **Logic**:
    *   `RunMain()` の `case lifecycle.Event:` で、画面破棄を表す `e.To == lifecycle.StageDead` のシグナルを捕捉し、以下のメッセージを `system` トピックへPublishします。
        ```go
        if e.To == lifecycle.StageDead {
            m.bus.Publish("system", &bus.BusMessage{
                Target:    bus.TargetSystem,
                Operation: bus.OpCommand,
                Data:      []byte(bus.CmdShutdown),
                Source:    m.Name(),
            })
        }
        ```

### cmd

#### [MODIFY] features/neurom/cmd/main.go
*   **Description**: Headless モードでのアプリ実行時に、ZMQからの連携を通してプロセスをクリーンアップさせる制御を追加します。
*   **Logic**:
    *   Headless モード時 (`if *headless`) に行っている `<-sigCh` 待機の箇所にて、`b.Subscribe("system")` 済みのチャネル (`sysCh`) も監視対象とする `select` に書き換えます。
    *   `sysCh` からシャットダウン要求を受領した場合もOS割り込みと同様に待機を抜け、`mgr.StopAll()` を呼び出す一連のクリーンアップシーケンスへ進行させます。

### tests

#### [NEW] features/neurom/integration/shutdown_test.go
*   **Description**: バスへ System Shutdown を送出することで、モジュール群がハングアップせず終了できるかの結合テストを追加。
*   **Logic**:
    *   1. Headless モードでモジュール類を `mgr.StartAll()` を用いて稼働させる。
    *   2. 外部から人工的に `"system"` トピックに対し `bus.CmdShutdown` メッセージを Publish する（ZMQの伝搬用として100msほど待つ）。
    *   3. `mgr.StopAll()` を最後呼び出し、デッドロックを起こさずに全Goroutineのシャットダウンが成功し正常に `PASS` するかを検証する。

## Step-by-Step Implementation Guide

1.  [x] **Update bus/message.go**:
    *   Edit `features/neurom/internal/bus/message.go` to add `TargetSystem` and `CmdShutdown` constants.
2.  [x] **Update monitor**:
    *   Edit `features/neurom/internal/modules/monitor/monitor.go` to add `sync.WaitGroup` to struct, `sysCh` listener in `receiveLoop`, `wg.Wait()` in `Stop`, and shutdown publish logic in `RunMain`.
3.  [x] **Update vram and io**:
    *   Edit `features/neurom/internal/modules/vram/vram.go` and `io.go` to apply the `sync.WaitGroup` termination pattern and add `system` listener selection inside event loops.
4.  [x] **Update cpu**:
    *   Edit `features/neurom/internal/modules/cpu/cpu.go` to add `sync.WaitGroup` handling pattern and system topic shutdown listener in `c.run`.
5.  [x] **Update cmd/main.go**:
    *   Edit `features/neurom/cmd/main.go` headless branch to subscribe to `"system"` and gracefully breakout of wait condition.
6.  [x] **Create Integration test**:
    *   Create `features/neurom/integration/shutdown_test.go` and execute verification.

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
    ./scripts/process/integration_test.sh --specify "shutdown_test.go"
    ```
    *   **Log Verification**: ログ上にコンパイルエラーが出力されないこと。また、`shutdown_test.go` 実行時に `StopAll()` 内のモジュールの待ち合わせがブロック（ハングアップ）せずにスムーズに完了し `PASS` が出力されることを確認します。

## Documentation

#### [MODIFY] prompts/phases/000-foundation/ideas/main/005-GracefulShutdown.md
*   **更新内容**: 特になし（本要件で定数化や要件対比を達成しているため、仕様に対する修正差分はなく現状を正とします）。
