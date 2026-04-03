# 000-RetroGameBus-ModularArchitecture

> **Source Specification**: prompts/phases/000-foundation/ideas/main/000-RetroGameBus-ModularArchitecture.md

## Goal Description
レトロゲーム機の拡張空間機能のように、独立した各仮想ハードウェアモジュールが、ZeroMQによるPub/Subベースのシステムバス等を通じて相互通信できる基本アーキテクチャ(Metov VM)を実装する。また、CPU、VRAM、ディスプレイモニタ、I/Oといった基本的なモジュールを準備し、抽象化された描画コマンドとHeadless制御に対応させる。これにより、柔軟な機能拡張基盤の確証を得る。

## User Review Required
None.

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| R-001: ZeroMQ を用いたデュアルバインディング対応のシステムバス | `Proposed Changes > features/vm/internal/bus` |
| R-002: 抽象コマンドによるメッセージ送受信 (メモリマップドI/O) | `Proposed Changes > features/vm/internal/bus/message.go` |
| R-003: ModuleManager によるライフサイクル管理 | `Proposed Changes > features/vm/internal/module` |
| R-004(1): CPUコアモジュール | `Proposed Changes > features/vm/internal/modules/cpu` |
| R-004(2): VRAMモジュール (パレット・解像度管理) | `Proposed Changes > features/vm/internal/modules/vram` |
| R-004(3): ディスプレイモニタモジュール (Headless, gomobile基盤) | `Proposed Changes > features/vm/internal/modules/monitor` |
| R-004(4): I/Oモジュール | `Proposed Changes > features/vm/internal/modules/io` |
| R-006: シングルバイナリ配布 (`go build`) | `Proposed Changes > features/vm/cmd` |

## Proposed Changes

### Bus (ZeroMQ Messaging)

#### [NEW] features/vm/internal/bus/bus.go
*   **Description**: システムバスのインターフェース定義
*   **Technical Design**:
    *   ```go
        package bus
        type Bus interface {
            Publish(topic string, msg *BusMessage) error
            Subscribe(topic string) (<-chan *BusMessage, error)
            Close() error
        }
        ```

#### [NEW] features/vm/internal/bus/message_test.go
*   **Description**: BusMessage のシリアライズ単体テスト
*   **Logic**:
    *   Operation が正しいか、データのエンコード/デコードが正確かを確認

#### [NEW] features/vm/internal/bus/message.go
*   **Description**: 仕様書のメッセージフォーマット実装
*   **Technical Design**:
    *   ```go
        package bus
        // BusMessage はシステムバスを流れるメッセージの構造体
        type BusMessage struct {
            Target    string    // ターゲット指定 (アドレス/コマンド等)
            Operation OpType    // Write or Read or Command
            Data      []byte    // データペイロード
            Source    string    // 送信元モジュール名
        }
        
        type OpType uint8
        const (
            OpWrite OpType = iota
            OpRead
            OpCommand
        )
        // Encode / Decode func
        ```

#### [NEW] features/vm/internal/bus/zmqbus_test.go
*   **Description**: ZMQバス実装の単体テスト
*   **Logic**:
    *   インメモリでパブリッシャ/サブスクライバを起動し、同じトピックでのメッセージ到達を確認

#### [NEW] features/vm/internal/bus/zmqbus.go
*   **Description**: `go-zmq4` による `Bus` インターフェース実装
*   **Technical Design**:
    *   `NewZMQBus(inprocAddr, tcpAddr string)` 実装。
    *   送信側と受信側のソケットを管理し、`Publish`/`Subscribe` ルーチンを持つ。

### Module Framework

#### [NEW] features/vm/internal/module/module.go
*   **Description**: モジュールの共通インターフェース
*   **Technical Design**:
    *   ```go
        package module
        import "context"
        import "github.com/axsh/neurom/internal/bus"
        
        type Module interface {
            Name() string
            Start(ctx context.Context, b bus.Bus) error
            Stop() error
        }
        ```

#### [NEW] features/vm/internal/module/manager_test.go
*   **Description**: モジュール管理の単体テスト
*   **Logic**:
    *   モジュール登録、Start()伝搬、Stop()の正常挙動確認。

#### [NEW] features/vm/internal/module/manager.go
*   **Description**: ModuleManagerの実装
*   **Technical Design**:
    *   `Register(m Module)`, `StartAll(ctx context.Context)`, `StopAll()` を持つManager構造体。

### Standard Modules

#### [NEW] features/vm/internal/modules/cpu/cpu_test.go
*   **Description**: CPUモジュールのバス書き込みテスト
#### [NEW] features/vm/internal/modules/cpu/cpu.go
*   **Description**: CPUモジュール実装
*   **Logic**:
    *   ダミーの演算ループを回し、起動直後に VRAM向け `SetMode(256, 212, 8)` の `OpCommand` を Publish する挙動等を実装。

#### [NEW] features/vm/internal/modules/vram/vram_test.go
*   **Description**: VRAMのパレットやピクセル更新テスト
#### [NEW] features/vm/internal/modules/vram/vram.go
*   **Description**: VRAMモジュール実装
*   **Logic**:
    *   バスから `OpCommand` の `SetMode`, `DrawPixel`, `BlockTransfer`, `UpdatePalette` 等を受信。
    *   `SetMode(width, height, colorBit)` コマンドで内部のパレットマップとピクセルバッファバリアントを再計算（初期パレット生成）。
    *   更新後、`bus.Publish("vram_update", EventMsg)` のようにバッファ更新イベントを発行。

#### [NEW] features/vm/internal/modules/monitor/monitor_test.go
*   **Description**: モニタモジュールテスト (Headless時のスキップ確認)
#### [NEW] features/vm/internal/modules/monitor/monitor.go
*   **Description**: ディスプレイモニタ実装
*   **Logic**:
    *   起動時、`MonitorConfig{Headless: bool}` を受ける。
    *   Headless==true なら `go-mobile` (`golang.org/x/mobile/app` 等) 等のGUI立ち上げを呼ばず、メッセージ受信ループだけで描画処理本体をスキップ・ログ記録する。
    *   Headless==false の場合、別スレッドでイベントループを開始し、`vram_update` メッセージをバスから受け取ったら該当領域の画面再描画をコールする。

#### [NEW] features/vm/internal/modules/io/io.go
*   **Description**: Input/Outputモジュール (スケルトン)

### Application Entry

#### [MODIFY] features/vm/cmd/main.go
*   **Description**: メイン実行ファイル
*   **Technical Design**:
    *   `flag` で `--headless` や `--tcp-port` 等を受け付ける。
    *   `bus.NewZMQBus()`, `module.NewManager()` 初期化。
    *   CPU, VRAM, Monitor, I/O 等を Register し、ManagerをStartさせる。

### Integration Tests

#### [NEW] features/vm/integration/vram_monitor_test.go
*   **Description**: 複数モジュールの結合連携テスト
*   **Logic**:
    *   実 Bus (ZMQ inproc) と全モジュール（Monitorは Headless=true で実行）を Start。
    *   CPUモジュールからメッセージ発行させ、暫くのち VRAM内部ステータスが更新されたか、モニタが受け取れたかをバスのログイベントなどから確認する。

## Step-by-Step Implementation Guide

1.  **[x] [Step 1: Setup Message Bus]**:
    *   `features/vm/internal/bus/` の各種ファイルを作成し、`BusMessage`とZMQバス（`zmqbus.go`）を実装する。単体テスト `zmqbus_test.go` 等を記述する。
2.  **[x] [Step 2: Module Manager]**:
    *   `features/vm/internal/module/module.go`, `manager.go` およびそのテストを作成。
3.  **[x] [Step 3: Core Domain (CPU & VRAM)]**:
    *   `cpu.go` および `vram.go` を作成。VRAM内にコマンド種別（SetMode等）に対応する処理とピクセルバッファを実装。
4.  **[x] [Step 4: Display Monitor]**:
    *   `monitor.go` を実装。`--headless`対応と `go-mobile` 基盤スケルトンを準備する。
5.  **[x] [Step 5: Wiring & Entrypoint]**:
    *   `cmd/main.go` を改修し、全てのコンポーネントを接続して起動させる。
6.  **[x] [Step 6: Integration Test]**:
    *   `integration/vram_monitor_test.go` を実装し検証を行う。

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
    ./scripts/process/integration_test.sh --specify "integration/vram_monitor_test.go"
    ```
    *   **Log Verification**: ログ上でCPUからPublishされたメッセージが、VRAMモジュールに到達し、ModeChanged及びVRAMUpdatedイベントがMonitorモジュールに処理（Headlessなのでスキップ）された旨の出力があるか確認。

## Documentation

#### [MODIFY] features/README.md
*   **更新内容**: `features/vm` 以下の本フェーズ（RetroGameBus アーキテクチャと各モジュール）のディレクトリ構造や実行方法（`--headless` 等）の記載を追記する。
