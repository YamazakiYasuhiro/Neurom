# 007-ZMQBusPanicGuard

> **Source Specification**: [prompts/phases/000-foundation/ideas/main/007-ZMQBusPanicGuard.md](file://prompts/phases/000-foundation/ideas/main/007-ZMQBusPanicGuard.md)

## Goal Description

zmq4 ライブラリの TCP フレーム解析時に発生する `makeslice: len out of range` panic を、トランスポートの分離（inproc 専用化）と TCP ブリッジの隔離により解消する。併せて、バス層にチャンク送信プロトコルを導入し、大規模メッセージの安全な転送を透過的に実現する。

## User Review Required

None.

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| R1: 内部通信と外部通信のトランスポート分離 | Proposed Changes > `zmqbus.go` (MODIFY) + `cmd/main.go` (MODIFY) |
| R2: TCP エンドポイントの隔離 | Proposed Changes > `tcpbridge.go` (NEW) |
| R3: バス起動時に panic しないこと | Proposed Changes > `zmqbus.go` (MODIFY, inproc only) + `zmqbus_test.go` |
| R4: TCP の graceful degradation | Proposed Changes > `tcpbridge.go` (NEW) |
| R5: 後方互換性 | `bus.Bus` インターフェース不変。全テストで検証 |
| R6: バス層でのチャンク送信 | Proposed Changes > `chunk.go` (NEW) + `zmqbus.go` (MODIFY) |
| R7: 最大フレームサイズのセーフガード (任意) | 先送り。zmq4 にソケットオプションがなく、フォークなしでは実装不可。チャンク送信 (R6) が実質的な代替策となる |
| R8: TCP ブリッジ無効化フラグ (任意) | Proposed Changes > `cmd/main.go` (MODIFY) — `--no-tcp` フラグ |

## Proposed Changes

### bus パッケージ

#### [NEW] [features/neurom/internal/bus/chunk_test.go](file://features/neurom/internal/bus/chunk_test.go)
*   **Description**: チャンクエンコード・デコードロジックの単体テスト。
*   **Technical Design**:
    ```go
    func TestEncodeChunks_SmallPayload(t *testing.T)
    func TestEncodeChunks_ExactThreshold(t *testing.T)
    func TestEncodeChunks_LargePayload(t *testing.T)
    func TestEncodeChunks_EmptyPayload(t *testing.T)
    func TestIsChunkedFrame(t *testing.T)
    func TestChunkReassembler_Complete(t *testing.T)
    func TestChunkReassembler_Timeout(t *testing.T)
    func TestChunkReassembler_DuplicateChunk(t *testing.T)
    ```
*   **Logic**:
    *   `TestEncodeChunks_SmallPayload`: 閾値以下のペイロードを渡すと `nil` (チャンク不要) が返ることを確認。
    *   `TestEncodeChunks_ExactThreshold`: ちょうど閾値サイズのペイロードは単一送信 (チャンク不要) となることを確認。
    *   `TestEncodeChunks_LargePayload`: 100 KB のペイロードを渡し、生成されたチャンク数が `ceil(100KB / 32KB) = 4` であること、各チャンクヘッダのフィールド（マジックバイト、メッセージ ID、インデックス、総数、総バイト数）が正しいこと、全チャンクのペイロードを結合すると元のデータと一致することを確認。
    *   `TestEncodeChunks_EmptyPayload`: 空ペイロードはチャンク不要で `nil` が返ることを確認。
    *   `TestIsChunkedFrame`: マジックバイト `0xC1` で始まるフレームは `true`、gob データは `false`。
    *   `TestChunkReassembler_Complete`: 全チャンクを順番に `Add` し、完成したバイト列が元データと一致することを確認。
    *   `TestChunkReassembler_Timeout`: `CleanExpired` を呼び出し、期限切れエントリが除去されることを確認。
    *   `TestChunkReassembler_DuplicateChunk`: 同じインデックスのチャンクを重複追加しても panic せず、結果が正しいことを確認。

#### [NEW] [features/neurom/internal/bus/chunk.go](file://features/neurom/internal/bus/chunk.go)
*   **Description**: チャンクプロトコルのエンコード・デコード・再組み立てロジック。
*   **Technical Design**:
    ```go
    package bus

    const (
        ChunkMagic     byte   = 0xC1
        ChunkHeaderLen        = 17
        ChunkThreshold        = 32 * 1024 // 32 KB
    )

    // chunkHeader represents the fixed-size header prepended to each chunk frame.
    type chunkHeader struct {
        MessageID  uint64
        ChunkIndex uint16
        TotalCount uint16
        TotalSize  uint32
    }

    // encodeChunkHeader serializes a chunkHeader into ChunkHeaderLen bytes.
    // Format: [0xC1][msgID:u64be][idx:u16be][total:u16be][size:u32be]
    func encodeChunkHeader(h chunkHeader) []byte

    // decodeChunkHeader parses a chunk header from raw bytes.
    // Returns error if len < ChunkHeaderLen or magic byte mismatch.
    func decodeChunkHeader(data []byte) (chunkHeader, error)

    // IsChunkedFrame returns true if data starts with ChunkMagic.
    func IsChunkedFrame(data []byte) bool

    // EncodeChunks splits payload into chunks if it exceeds ChunkThreshold.
    // Returns nil if no chunking is needed (payload <= threshold).
    // Each returned []byte is a complete chunk frame (header + payload slice).
    func EncodeChunks(msgID uint64, payload []byte) [][]byte

    // reassemblyEntry holds in-progress chunk reassembly state.
    type reassemblyEntry struct {
        chunks    [][]byte   // indexed by ChunkIndex
        received  int        // count of received chunks
        total     int        // TotalCount from header
        totalSize int        // TotalSize from header
        createdAt time.Time
    }

    // ChunkReassembler manages reassembly of chunked messages.
    type ChunkReassembler struct {
        mu      sync.Mutex
        pending map[uint64]*reassemblyEntry
        timeout time.Duration // default 5s
    }

    func NewChunkReassembler(timeout time.Duration) *ChunkReassembler

    // Add processes a single chunk frame.
    // Returns (assembled payload, true) when all chunks are received.
    // Returns (nil, false) if more chunks are needed.
    func (r *ChunkReassembler) Add(data []byte) ([]byte, bool)

    // CleanExpired removes entries older than timeout.
    func (r *ChunkReassembler) CleanExpired()
    ```
*   **Logic**:
    *   `EncodeChunks`: `len(payload) <= ChunkThreshold` なら `nil` を返す。超過時は `ceil(len / ChunkThreshold)` 個のチャンクを生成。各チャンクフレームは `encodeChunkHeader(h) + payload[start:end]`。
    *   `Add`: `decodeChunkHeader` でヘッダを解析。`pending[msgID]` がなければ新規 `reassemblyEntry` を作成（`chunks = make([][]byte, totalCount)`）。`chunks[idx]` にペイロード部分を格納。`received == total` になったら全チャンクを結合して返却し、エントリを削除。
    *   `CleanExpired`: `pending` を走査し、`time.Since(createdAt) > timeout` のエントリを削除。

#### [MODIFY] [features/neurom/internal/bus/zmqbus_test.go](file://features/neurom/internal/bus/zmqbus_test.go)
*   **Description**: 既存テストの維持 + チャンク送信・inproc 専用のテスト追加。
*   **Technical Design**:
    ```go
    // Existing test (unchanged)
    func TestZMQBus(t *testing.T)

    // New tests
    func TestZMQBus_InprocOnly(t *testing.T)
    func TestZMQBus_NoPanicOnStartup(t *testing.T)
    func TestZMQBus_ChunkSmallMessage(t *testing.T)
    func TestZMQBus_ChunkLargeMessage(t *testing.T)
    func TestZMQBus_ChunkBoundary(t *testing.T)
    ```
*   **Logic**:
    *   `TestZMQBus_InprocOnly`: `NewZMQBus("inproc://test-inproc-only")` で作成。Subscribe → Publish → 受信を確認。既存 `TestZMQBus` と類似だが、エンドポイントが 1 つだけであることを明示的にテスト。
    *   `TestZMQBus_NoPanicOnStartup`: ループ 10 回で `NewZMQBus` → `Close` を繰り返す。panic が発生しないことを確認（各回で異なる inproc エンドポイント名を使用）。
    *   `TestZMQBus_ChunkSmallMessage`: 小さいデータ（100 bytes）の `BusMessage` を Publish し、Subscribe 側で完全に受信できることを確認。チャンク分割されないパス。
    *   `TestZMQBus_ChunkLargeMessage`: 100 KB の `Data` を持つ `BusMessage` を Publish し、Subscribe 側で受信したメッセージの `Data` が元と完全一致することを確認。チャンク分割・再組み立てパス。
    *   `TestZMQBus_ChunkBoundary`: `ChunkThreshold` ちょうど、`ChunkThreshold + 1`、空 `Data` の 3 パターンで Publish → Subscribe → 比較。テーブル駆動テスト。

#### [MODIFY] [features/neurom/internal/bus/zmqbus.go](file://features/neurom/internal/bus/zmqbus.go)
*   **Description**: チャンク送信対応の追加。構造体に `msgIDCounter` と `reassembler` を追加。`Publish` でチャンク分割、`receiveLoop` でチャンク再組み立て。
*   **Technical Design**:
    ```go
    type ZMQBus struct {
        // ... existing fields unchanged ...

        msgIDCounter atomic.Uint64      // chunk message ID counter
        reassembler  *ChunkReassembler  // chunk reassembly buffer
    }
    ```
    *   `NewZMQBus`: 引数のバリデーション変更なし。`reassembler` を `NewChunkReassembler(5 * time.Second)` で初期化。reassembly クリーンアップ用の goroutine を起動（10 秒間隔で `CleanExpired` を呼ぶ、`subCtx` でキャンセル）。
    *   `Publish` の変更:
        ```go
        func (b *ZMQBus) Publish(topic string, msg *BusMessage) error {
            data, err := msg.Encode()
            if err != nil {
                return err
            }

            chunks := EncodeChunks(b.msgIDCounter.Add(1), data)
            if chunks == nil {
                // Small message: send as-is (existing behavior)
                zm := zmq4.NewMsgFrom([]byte(topic), data)
                return b.pubSocket.Send(zm)
            }

            // Large message: send each chunk as a separate ZMQ message
            for _, chunk := range chunks {
                zm := zmq4.NewMsgFrom([]byte(topic), chunk)
                if err := b.pubSocket.Send(zm); err != nil {
                    return err
                }
            }
            return nil
        }
        ```
    *   `receiveLoop` の変更:
        ```go
        func (b *ZMQBus) receiveLoop() {
            for {
                zmsg, err := b.subSocket.Recv()
                if err != nil {
                    return
                }
                if len(zmsg.Frames) < 2 {
                    continue
                }

                topic := string(zmsg.Frames[0])
                data := zmsg.Frames[1]

                var decoded *BusMessage

                if IsChunkedFrame(data) {
                    assembled, complete := b.reassembler.Add(data)
                    if !complete {
                        continue
                    }
                    decoded, err = Decode(assembled)
                } else {
                    decoded, err = Decode(data)
                }

                if err != nil {
                    continue
                }

                b.dispatch(topic, decoded)
            }
        }
        ```
    *   `dispatch`: 既存の `receiveLoop` 内のロック + チャネル配信ロジックをメソッドとして抽出。
        ```go
        func (b *ZMQBus) dispatch(topic string, msg *BusMessage) {
            b.mu.Lock()
            defer b.mu.Unlock()
            for subTopic, chans := range b.subs {
                if strings.HasPrefix(topic, subTopic) {
                    for _, ch := range chans {
                        select {
                        case ch <- msg:
                        default:
                        }
                    }
                }
            }
        }
        ```

#### [NEW] [features/neurom/internal/bus/tcpbridge_test.go](file://features/neurom/internal/bus/tcpbridge_test.go)
*   **Description**: TCP ブリッジの単体テスト。
*   **Technical Design**:
    ```go
    func TestTCPBridge_StartStop(t *testing.T)
    func TestTCPBridge_PanicRecovery(t *testing.T)
    func TestTCPBridge_MaxRestartsExceeded(t *testing.T)
    ```
*   **Logic**:
    *   `TestTCPBridge_StartStop`: inproc バスを作成し、`NewTCPBridge` で TCP ブリッジを起動。即座に `Close` して正常終了を確認。
    *   `TestTCPBridge_PanicRecovery`: `TCPBridge` に `panicOnNextRecv` のようなテスト用フックを設定。ブリッジ goroutine が panic しても `recover()` で捕捉され、プロセスが生存し、ブリッジが再起動することを確認。
    *   `TestTCPBridge_MaxRestartsExceeded`: 最大再起動回数（例: 3）を設定。連続 panic 後に再起動が停止し、エラーがログに記録されることを確認。

#### [NEW] [features/neurom/internal/bus/tcpbridge.go](file://features/neurom/internal/bus/tcpbridge.go)
*   **Description**: TCP エンドポイントを inproc バスから隔離するブリッジコンポーネント。
*   **Technical Design**:
    ```go
    package bus

    // TCPBridgeConfig holds configuration for the TCP bridge.
    type TCPBridgeConfig struct {
        InprocEndpoint string // inproc endpoint to bridge to (e.g. "inproc://system-bus")
        TCPEndpoint    string // TCP endpoint to listen on (e.g. "tcp://127.0.0.1:5555")
        MaxRestarts    int    // max consecutive restart attempts (default: 5)
    }

    // TCPBridge bridges messages between an inproc ZMQ bus and a TCP endpoint.
    type TCPBridge struct {
        config    TCPBridgeConfig
        ctx       context.Context
        cancel    context.CancelFunc
        wg        sync.WaitGroup
        mu        sync.Mutex
        running   bool
        restarts  int
    }

    func NewTCPBridge(cfg TCPBridgeConfig) *TCPBridge

    // Start begins the bridge goroutine with panic recovery.
    func (tb *TCPBridge) Start() error

    // Close gracefully shuts down the bridge.
    func (tb *TCPBridge) Close() error

    // run is the internal bridge loop, wrapped in recover().
    // Creates its own PUB (TCP) and SUB (inproc) sockets.
    // Reads from SUB and forwards to PUB.
    func (tb *TCPBridge) run()

    // runWithRecovery wraps run() in a defer/recover loop.
    // On panic: logs the error, increments restarts counter,
    //   and restarts if under MaxRestarts limit.
    func (tb *TCPBridge) runWithRecovery()
    ```
*   **Logic**:
    *   `runWithRecovery`:
        ```
        for {
            func() {
                defer func() {
                    if r := recover(); r != nil {
                        log.Printf("[TCPBridge] recovered from panic: %v", r)
                        tb.restarts++
                    }
                }()
                tb.run()
            }()

            if tb.ctx.Err() != nil {
                return  // context canceled, normal shutdown
            }
            if tb.restarts >= tb.config.MaxRestarts {
                log.Printf("[TCPBridge] max restarts exceeded, giving up")
                return
            }
            time.Sleep(500 * time.Millisecond)  // backoff before restart
        }
        ```
    *   `run`: inproc SUB → TCP PUB の片方向転送。TCP 上の外部クライアントからのメッセージ受信が必要な場合は、逆方向の PUB/SUB ペアも追加可能だが、本計画では片方向（内部 → 外部）のみ。

### cmd パッケージ

#### [MODIFY] [features/neurom/cmd/main.go](file://features/neurom/cmd/main.go)
*   **Description**: バスを inproc 専用に変更し、TCP ブリッジをオプションで起動する。
*   **Technical Design**:
    *   `--no-tcp` フラグを追加。
    *   `NewZMQBus` の呼び出しを `inproc://` エンドポイントのみに変更。
    *   `--no-tcp` が指定されていない場合のみ `TCPBridge` を起動。
    ```go
    func main() {
        headless := flag.Bool("headless", false, "Run without display monitor")
        tcpPort := flag.String("tcp-port", "5555", "TCP port for external bus connections")
        noTCP := flag.Bool("no-tcp", false, "Disable TCP bridge for external connections")
        flag.Parse()

        log.Println("Starting Neurom (ARC Emulator)...")

        endpointInproc := "inproc://system-bus"

        b, err := bus.NewZMQBus(endpointInproc)
        // ... error handling ...
        defer b.Close()

        // Optional TCP bridge
        if !*noTCP {
            bridge := bus.NewTCPBridge(bus.TCPBridgeConfig{
                InprocEndpoint: endpointInproc,
                TCPEndpoint:    "tcp://127.0.0.1:" + *tcpPort,
                MaxRestarts:    5,
            })
            if err := bridge.Start(); err != nil {
                log.Printf("TCP bridge failed to start: %v (continuing without TCP)", err)
            } else {
                defer bridge.Close()
            }
        }

        // ... rest unchanged ...
    }
    ```

### 結合テスト

#### [NEW] [features/neurom/integration/bus_panic_guard_test.go](file://features/neurom/integration/bus_panic_guard_test.go)
*   **Description**: バス panic 防止の結合テスト。全モジュールを起動し、panic なしで動作することを確認。
*   **Technical Design**:
    ```go
    func TestBusPanicGuard_InprocStartupNoPanic(t *testing.T)
    func TestBusPanicGuard_ChunkLargeVRAMMessage(t *testing.T)
    ```
*   **Logic**:
    *   `TestBusPanicGuard_InprocStartupNoPanic`: `NewZMQBus("inproc://...")` で起動 → 全モジュール登録（headless） → `StartAll` → `time.Sleep(500ms)` → shutdown → `StopAll` を 3 回繰り返す。panic なしで完了することを確認。
    *   `TestBusPanicGuard_ChunkLargeVRAMMessage`: inproc バスを作成。VRAM モジュールを起動し、大きな `blit_rect` コマンド（256×212 = 54,272 ピクセル）を CPU 側から Publish。Monitor 側の Subscribe で `rect_updated` イベントが正しく受信されることを確認。チャンク送信が透過的に動作することの結合テスト。

## Step-by-Step Implementation Guide

> **Implementation Note**: 実装中に zmq4 v0.17.0 の inproc トランスポートに根本的なバグを発見。
> `conn.Read()` が Write より小さいバッファで呼ばれた場合に余剰データを破棄し、ZMTP ストリームの同期を失う。
> 255 バイトを超えるフレーム（long format）で必ず発生するため、内部バスを Go チャネルベースの `ChannelBus` に置き換えた。
> zmq4 は TCP ブリッジ専用に限定。

### Phase 1: チャンクプロトコル

1.  [x] **chunk_test.go を作成**:
    *   `features/neurom/internal/bus/chunk_test.go` を新規作成。
    *   `TestEncodeChunks_SmallPayload`, `TestEncodeChunks_ExactThreshold`, `TestEncodeChunks_LargePayload`, `TestEncodeChunks_EmptyPayload` を実装。
    *   `TestIsChunkedFrame` を実装。
    *   `TestChunkReassembler_Complete`, `TestChunkReassembler_Timeout`, `TestChunkReassembler_DuplicateChunk` を実装。

2.  [x] **chunk.go を作成**:
    *   `features/neurom/internal/bus/chunk.go` を新規作成。
    *   定数: `ChunkMagic = 0xC1`, `ChunkHeaderLen = 17`, `ChunkThreshold = 32 * 1024`。
    *   `chunkHeader` 構造体、`encodeChunkHeader`, `decodeChunkHeader` を実装。
    *   `IsChunkedFrame`, `EncodeChunks` を実装。
    *   `reassemblyEntry`, `ChunkReassembler`, `NewChunkReassembler`, `Add`, `CleanExpired` を実装。

### Phase 2: ChannelBus の作成（zmq4 inproc バグ回避）

3.  [x] **channelbus_test.go を作成**:
    *   `TestChannelBus_Basic`, `TestChannelBus_NoPanicOnStartup` を実装。
    *   `TestChannelBus_SmallMessage`, `TestChannelBus_LargeMessage`, `TestChannelBus_VeryLargeMessage` を実装。
    *   `TestChannelBus_Boundary`, `TestChannelBus_MultipleSubscribers` を実装。

4.  [x] **channelbus.go を作成**:
    *   純粋な Go チャネルベースのバス実装。
    *   `Bus` インターフェースを実装。
    *   zmq4 を一切使用しない。任意サイズのメッセージをサポート。

4b. [x] **zmqbus.go を TCP ブリッジ専用に変更**:
    *   チャンク関連のコードを削除（内部バスでは不要）。
    *   小さいメッセージ（< 255 bytes）のみ安全に送信可能なことをコメントに記載。

### Phase 3: TCP ブリッジ

5.  [x] **tcpbridge_test.go を作成**:
    *   `features/neurom/internal/bus/tcpbridge_test.go` を新規作成。
    *   `TestTCPBridge_StartStop`, `TestTCPBridge_PanicRecovery`, `TestTCPBridge_MaxRestartsExceeded` を実装。

6.  [x] **tcpbridge.go を作成**:
    *   `features/neurom/internal/bus/tcpbridge.go` を新規作成。
    *   `TCPBridgeConfig`, `TCPBridge` 構造体を実装。
    *   `NewTCPBridge`, `Start`, `Close`, `run`, `runWithRecovery` を実装。
    *   panic recovery + max restarts ロジックを含む。

### Phase 4: エントリポイント修正 + 結合テスト

7.  [x] **cmd/main.go を修正**:
    *   `--no-tcp` フラグを追加。
    *   `NewZMQBus` → `NewChannelBus` に変更。
    *   `--no-tcp` が未指定の場合のみ `TCPBridge` を起動。

7b. [x] **既存テストを ChannelBus に移行**:
    *   `shutdown_test.go`, `palette_test.go`, `vram_monitor_test.go`, `vram_enhancement_test.go` の全テストで `NewZMQBus` → `NewChannelBus` に変更。

8.  [x] **結合テストを作成**:
    *   `features/neurom/integration/bus_panic_guard_test.go` を新規作成。
    *   `TestBusPanicGuard_InprocStartupNoPanic`, `TestBusPanicGuard_ChunkLargeVRAMMessage`, `TestBusPanicGuard_LargeDataIntegrity` を実装。

9.  [x] **既存テストの回帰確認**:
    *   ビルドスクリプトを全体実行し、全テストが PASS であることを確認。

## Verification Plan

### Automated Verification

1.  **Build & Unit Tests**:
    Run the build script.
    ```bash
    ./scripts/process/build.sh
    ```
    *   **Log Verification**: 全ユニットテスト (`TestEncodeChunks_*`, `TestChunkReassembler_*`, `TestZMQBus_*`, `TestTCPBridge_*`) が `PASS` であること。

2.  **Integration Tests**:
    Run integration tests.
    ```bash
    ./scripts/process/integration_test.sh --specify "TestBusPanicGuard"
    ```
    *   **Log Verification**: `TestBusPanicGuard_InprocStartupNoPanic` と `TestBusPanicGuard_ChunkLargeVRAMMessage` が `PASS` であること。

3.  **Regression**:
    Run all integration tests.
    ```bash
    ./scripts/process/integration_test.sh
    ```
    *   **Log Verification**: 既存テスト (`TestGracefulShutdownViaBus`, `TestVRAMMonitorIntegration` 等) が引き続き `PASS` であること。

## Documentation

#### [MODIFY] [prompts/phases/000-foundation/ideas/main/007-ZMQBusPanicGuard.md](file://prompts/phases/000-foundation/ideas/main/007-ZMQBusPanicGuard.md)
*   **更新内容**: 実装完了後、仕様書の「ライブラリの状況」セクションに対応済みであることを追記する。
