# 028: 外部バスの双方向化と言語中立エンベロープ

## 背景 (Background)

### 問題 1: 外部 TCP は「傍受専用ポート」でしかない

`ARC-Architecture.md` 27 行は次のように謳っている。

> **極めて強力なハックのしやすさ (Hackability)**: 通信バスが標準的なTCPやIPCソケットとして外部に開かれているため、
> サードパーティの自作モジュールや外部スクリプト（Pythonなど）を簡単に接続できます。
> これにより、内部メモリのリアルタイム監視、**外部からの強制的なステート書き換え（チート作成）**、
> 機械学習エージェントの接続、カスタムデバッガの開発など、システム内部への干渉が容易に行える設計となっています。

しかし `features/neurom/internal/bus/tcpbridge.go` の実装（104-152 行）は
**PUB ソケット 1 個だけ**である。

```go
pub := zmq4.NewPub(pubCtx)
if err := pub.Listen(tb.config.TCPEndpoint); err != nil { ... }

ch, err := tb.config.Bus.Subscribe("")   // ChannelBus の全メッセージを購読

for {
	select {
	case msg, ok := <-ch:
		data, err := msg.Encode()
		zm := zmq4.NewMsgFrom([]byte(msg.Target), data)
		if err := pub.Send(zm); err != nil { ... }   // ← 外向きのみ
	}
}
```

受信側の `zmq4.NewSub` / `Recv()` は `internal/bus/zmqbus.go` にしか存在せず、
その `ZMQBus` は `cmd/main.go` から**もう使われていない**（`bus.NewChannelBus()` に置換済み）。

つまり **外部プロセスからバスへコマンドを投入する経路が物理的に存在しない**。
リアルタイム監視・リモートモニタは実装可能だが、
**ステート書き換え・機械学習エージェント・拡張ソフトウェアはいずれも動かせない**。
仕様が謳う機能の半分が未実装である。

なお `ideas/000-RetroGameBus-ModularArchitecture.md` 75-79 行（R-005）でも
外部バス活用は「**本フェーズでは設計のみ**」と明記されており、その後の設計文書は書かれていない。

### 問題 2: エンベロープが Go 専用フォーマット

`features/neurom/internal/bus/message.go` 32-51 行:

```go
func (m *BusMessage) Encode() ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(m); err != nil { ... }
```

`encoding/gob` は Go 専用のシリアライズ形式であり、
`ARC-Architecture.md` 26 行の「**多言語・分散対応**」と正面から矛盾する。

幸い影響範囲は限定的である。

- `Data` フィールドの中身（VRAM コマンドの payload）は**素の big-endian バイト列**であり、
  すでに言語中立である。
- `ChannelBus` は `Encode` を呼ばない（ポインタをそのまま渡す）。
- したがって `gob` 依存は **TCP 経路とそのテストだけ**である。

当初仕様でも変更が想定されていた
（`ideas/000-RetroGameBus-ModularArchitecture.md` 94 行:
「`encoding/binary` | メッセージシリアライズ | 初期実装用。将来はMessagePack等を適宜検討。」）。

### 問題 3: チャンク分割機構が配線されていない

`features/neurom/internal/bus/chunk.go` は 32KB 閾値の分割・再組立を完全に実装しているが、
**どのトランスポートからも呼ばれていない死んだコード**である。
`EncodeChunks` / `ChunkReassembler` の呼び出し元は `chunk_test.go` のみ。

```
features/neurom/internal/bus/chunk.go       ← 実装のみ
features/neurom/internal/bus/chunk_test.go  ← 唯一の呼び出し元
```

in-process の `ChannelBus` はポインタを渡すだけなので分割は不要だが、
TCP 経路では必要になる。512×512 ページの全面転送は 262KB を超え、
`ChunkThreshold`（32KB）の 9 倍に達する。

さらに `ChunkReassembler.CleanExpired()` も**誰も呼んでいない**。
これを呼ばないと、チャンクを取りこぼした受信側で
`pending` マップが永久に残り**メモリリークになる**。

### 問題 4: 電線上のトピックが内部トピックと一致していない

TCPBridge は ZMQ のトピックフレームに `msg.Target` を入れている（`tcpbridge.go` 144 行）。

```go
topic := msg.Target
zm := zmq4.NewMsgFrom([]byte(topic), data)
```

一方、内部バスの `Publish(topic, msg)` の `topic` は `vram` や `vram_update` である。
したがって外部購読者は `vram_update` ではなく **`rect_updated` や `page_error` といった
Target 名で購読する**ことになり、内部と外部でアドレッシングのセマンティクスが異なる。

025 でトピックを階層命名（`cmd.vram` / `evt.vram`）へ改称するため、
このずれを解消する好機である。

### 問題 5: 外部入力に対する防御が存在しない

現状は受信経路が無いため問題になっていないが、双方向化すると
**外部から任意のバイト列が届く**ようになる。
`vram.go` の各 `handle*` は長さ不足を検査するが、
`set_page_size` は `w`・`h` をそのまま `make([]uint8, w*h)` に渡している（`vram.go` 710-716 行）。

```go
w := int(binary.BigEndian.Uint16(msg.Data[1:]))
h := int(binary.BigEndian.Uint16(msg.Data[3:]))
pg := &v.pages[page]
pg.width = w
pg.height = h
pg.index = make([]uint8, w*h)
pg.color = make([]uint8, w*h*4)
```

`w = h = 65535` を送られると `65535 × 65535 × 4` ≒ **17GB** の確保を試みて
プロセスが死ぬ。外部公開の前に上限検証が必要である。

### 本仕様の位置づけ

拡張ソフトウェア分離（024〜029）の第 5 ステップ。
**基盤側に「外部プロセスが接続できる口」を正式に開ける。**

- 前提: [024-ARCProtocol-PackageExtraction](file://prompts/phases/000-foundation/branches/main/ideas/024-ARCProtocol-PackageExtraction.md)（プロトコル定義）
- 前提: [025-BusProtocol-BatchAndAddressing](file://prompts/phases/000-foundation/branches/main/ideas/025-BusProtocol-BatchAndAddressing.md)（トピック階層、相関 ID）
- 027 とは**独立に実施可能**（SDK の有無に関わらず成立する）
- 後続: 029（`arc.Dial` と Metov の別バイナリ化）

## 要件 (Requirements)

### 必須要件

1. **R1: 受信経路の新設**
   - `TCPBridge` に外部からメッセージを受け取る経路を追加し、
     受信したメッセージを内部の `ChannelBus` へ publish する。
   - ソケットパターンは PUSH/PULL を採用する（PULL 側を Neurom がバインドする）。
     理由は「実現方針」に記載。
   - 送信（PUB）と受信（PULL）は**別ポート**とし、既定値と CLI フラグを定める。
   - 受信経路の障害が送信経路を停止させないこと。

2. **R2: 言語中立エンベロープへの置換**
   - `BusMessage` のシリアライズを `encoding/gob` から**明示的なバイナリレイアウト**へ置換する。
   - 固定長ヘッダ + 可変長フィールドの構成とし、全整数をビッグエンディアンで表現する。
   - プロトコルバージョンフィールドを含め、将来の変更に備える。
   - **他言語（Python など）から仕様書だけを見て実装できること**を合格条件とする。
   - 025 で追加した相関 ID をエンベロープに含めること。

3. **R3: 電線上のアドレッシングの統一**
   - ZMQ のトピックフレームには、内部バスの**トピック**（025 の階層命名）を入れる。
     `msg.Target` を流用する現在の実装を改める。
   - 外部購読者が `evt.vram` のようなトピック接頭辞で購読でき、
     内部モジュールと同一のアドレッシング体系になること。
   - 階層マッチングが ZMQ の SUB 接頭辞フィルタと矛盾しないことを確認する
     （025 のセパレータ方式は ZMQ の接頭辞フィルタと素直に整合する）。

4. **R4: チャンク分割の配線**
   - 送信時、閾値を超えるメッセージを `EncodeChunks` で分割して送出する。
   - 受信時、`ChunkReassembler` で再組立してから内部バスへ流す。
   - **`CleanExpired()` を定期実行する janitor を設ける**（未配線のままではメモリリークになる）。
   - チャンクの一部が欠落した場合、タイムアウト後に破棄し、
     その事実をログと統計に記録すること（黙って消えないこと）。

5. **R5: 外部入力の検証**
   - 受信メッセージに対する上限を定義し、超過分を拒否する。
     - エンベロープ全体の最大バイト数
     - `Target` / `Source` 文字列の最大長
     - チャンク再組立の最大合計サイズ
   - `set_page_size` などページ確保を伴うコマンドについて、
     幅・高さ・総ピクセル数の上限を VRAM 側で検証し、
     超過時は確保を行わずエラーイベントを発行すること。
   - 不正なエンベロープ・不正な payload を受けても **panic せず**、
     ログに記録して当該メッセージのみ破棄すること。

6. **R6: 隔離特性の維持**
   - `ideas/007-ZMQBusPanicGuard.md` で確立した以下の性質を維持する。
     - 内部モジュール通信は TCP を経由しない
     - TCP 側の panic がエミュレータ全体を巻き込まない
     - 起動時に panic しない（ポート再利用・外部接続なしを含む）
     - TCP ブリッジの致命的エラー時は TCP のみを再試行またはシャットダウンする
   - 受信 goroutine も既存の `runWithRecovery` と同等の panic recovery 下に置くこと。
   - `MaxRestarts`（既定 5）の枠組みを受信側にも適用すること。

7. **R7: ハンドシェイクと能力照会**
   - 外部クライアントが接続直後に基盤の能力を照会できるコマンドを追加する。
   - 応答に含める項目:
     - プロトコルバージョン
     - 画面（既定ページ）の幅・高さ
     - 最大ページ数、最大ページサイズ
     - パレット段数
     - 対応コマンド ID の一覧
   - **拡張ソフトウェアに画面サイズをハードコードさせないこと**が目的である
     （現状 `cpu.go` 14-19 行が `VRAMWidth = 256` / `VRAMHeight = 212` を
     基盤の定数から独立にコピーしている）。
   - 応答は 025 の相関 ID で対応付ける。

8. **R8: 信頼モデルの明文化**
   - 既定のバインドアドレスをループバックに限定する（現状の `127.0.0.1` を維持）。
   - 外部公開アドレスへのバインドを許す場合は明示的なオプトインとし、
     **認証・認可を行わない**ことを仕様書に明記する。
   - ARC の設計思想は「バスに繋げた者は何でもできる」であり、
     これは意図的な設計である。その前提を文書化し、
     LAN 公開時のリスクを利用者が理解できるようにする。

9. **R9: 既存 CLI との互換**
   - `--no-tcp` による無効化、`--tcp-port` による送信ポート指定の意味を維持する。
   - `--no-tcp` 指定時は受信経路も起動しないこと。

10. **R10: プロトコル仕様書の更新**
    - 024 で作成した仕様書に、以下を追記する。
      - エンベロープのバイトレイアウト
      - チャンクフレームのレイアウト（`chunk.go` の既存実装に基づく）
      - トピック階層とアドレッシング規則
      - ソケットパターンと既定ポート
      - ハンドシェイクの手順
      - 上限値の一覧
      - 信頼モデル

### 任意要件

11. **R11: 参照クライアントスクリプト**
    - Python 等で書いた最小の接続確認スクリプトを用意し、
      仕様書だけから実装可能であることを実証する。
    - 置き場所は `scripts/utils/` 配下または `features/neurom/testdata/` を想定。

12. **R12: 外部接続の統計**
    - 接続中クライアント数、受信メッセージ数、拒否件数、チャンク再組立失敗件数を
      `/stats` から取得できるようにする。

13. **R13: `ZMQBus` の扱いの決定**
    - `internal/bus/zmqbus.go` は `cmd/main.go` から使われておらず、
      `zmqbus_test.go` のみが利用者である。
    - `023-ZMQBusPanicElimination.md` 100 行では「TCPBridge が依存するため変更不要」と
      記載されているが、実際には `tcpbridge.go` は `zmq4` を直接使っており
      `ZMQBus` に依存していない。この齟齬を解消し、
      `ZMQBus` を削除するか参照実装として残すかを決定する。

14. **R14: 送信側の輻輳対策**
    - ZMQ PUB は購読者が遅い場合に高水位標（HWM）を超えたメッセージを破棄する。
    - チャンク分割済みメッセージの一部だけが破棄されると再組立が永久に完了しないため、
      HWM の明示設定と、破棄検知の仕組みを検討する。

## 実現方針 (Implementation Approach)

### ソケットパターンの選択

| 方向 | パターン | Neurom 側 | 理由 |
|---|---|---|---|
| 送信（イベント） | PUB / SUB | PUB を bind | 複数クライアントへの同時配信。トピックフィルタが使える。現状踏襲 |
| 受信（コマンド） | PUSH / PULL | PULL を bind | 複数クライアントからの投入を公平キューで受けられる。購読管理が不要 |

REQ/REP や ROUTER/DEALER を採らない理由:
バスは本質的に fire-and-forget のメッセージパッシングであり、
要求・応答の対応付けは 025 の相関 ID が既に担っている。
PUSH/PULL の方が実装が単純で、片方向の障害が他方に波及しない。

既定ポート:

| 用途 | フラグ | 既定値 |
|---|---|---|
| イベント配信（PUB） | `--tcp-port` | 5555（現状維持） |
| コマンド受信（PULL） | `--tcp-cmd-port` | 5556 |

### エンベロープのバイトレイアウト

固定長ヘッダ 16 バイト + 可変長 3 フィールド。すべてビッグエンディアン。

```
オフセット  サイズ  フィールド      説明
0           1       magic          0xA1（ARC の識別子）
1           1       version        プロトコルバージョン。初版は 1
2           1       operation      OpWrite / OpRead / OpCommand
3           1       flags          予約（将来の圧縮フラグ等）。初版は 0
4           4       request_id     相関 ID（025）。0 は「対応付け不要」
8           2       target_len     Target 文字列のバイト長
10          2       source_len     Source 文字列のバイト長
12          4       data_len       Data のバイト長
16          可変    target         UTF-8
+           可変    source         UTF-8
+           可変    data           コマンド payload（既に言語中立）
```

`chunk.go` の既存チャンクヘッダ（`ChunkMagic = 0xC1`、17 バイト）とは
先頭バイトで区別できるため、両者は共存できる。
受信側は `IsChunkedFrame` で判別してから処理を分岐する（既存の関数がそのまま使える）。

`message.go` の `Encode` / `Decode` を差し替えるだけで済むため、
呼び出し元（`tcpbridge.go`、`zmqbus.go`、`message_test.go`）の
シグネチャ変更は不要である。

### TCPBridge の構造変更

```go
func (tb *TCPBridge) run() {
	ctx, cancel := context.WithCancel(tb.ctx)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); tb.runPublisher(ctx) }()   // 既存: 内部 → 外部
	go func() { defer wg.Done(); tb.runReceiver(ctx) }()    // 新規: 外部 → 内部
	go func() { defer wg.Done(); tb.runJanitor(ctx) }()     // 新規: CleanExpired 定期実行
	wg.Wait()
}
```

3 本とも `runWithRecovery` の panic recovery 配下に置かれるため R6 を満たす。
片方のソケットが失敗した場合の扱い（全体再起動か片側のみ縮退か）は
実装計画で決定する。

受信ループ:

```go
func (tb *TCPBridge) runReceiver(ctx context.Context) {
	pull := zmq4.NewPull(ctx)
	if err := pull.Listen(tb.config.CmdEndpoint); err != nil {
		log.Printf("[TCPBridge] failed to listen for commands on %s: %v", tb.config.CmdEndpoint, err)
		return
	}
	defer pull.Close()

	for {
		zm, err := pull.Recv()
		if err != nil {
			return
		}
		tb.handleInbound(zm)
	}
}

func (tb *TCPBridge) handleInbound(zm zmq4.Msg) {
	if len(zm.Frames) < 2 {
		tb.stats.rejected++
		return
	}
	topic := string(zm.Frames[0])
	payload := zm.Frames[1]

	// R5: 上限検証
	if len(payload) > MaxEnvelopeBytes {
		tb.stats.rejected++
		return
	}

	// R4: チャンク再組立
	if bus.IsChunkedFrame(payload) {
		assembled, done := tb.reassembler.Add(payload)
		if !done {
			return
		}
		payload = assembled
	}

	msg, err := bus.Decode(payload)
	if err != nil {
		tb.stats.rejected++
		return   // R5: panic せず破棄
	}

	// 外部由来であることを記録し、内部バスへ投入
	tb.config.Bus.Publish(topic, msg)
}
```

### チャンク分割の配線（R4）

送信側は閾値超過時に分割する。

```go
func (tb *TCPBridge) sendMessage(pub zmq4.Socket, topic string, msg *bus.BusMessage) error {
	payload, err := msg.Encode()
	if err != nil {
		return nil   // エンコード不能はスキップ（現状踏襲）
	}

	chunks := bus.EncodeChunks(tb.nextMsgID(), payload)
	if chunks == nil {
		// 閾値以下: そのまま 1 フレーム
		return pub.Send(zmq4.NewMsgFrom([]byte(topic), payload))
	}
	for _, c := range chunks {
		if err := pub.Send(zmq4.NewMsgFrom([]byte(topic), c)); err != nil {
			return err
		}
	}
	return nil
}
```

janitor は `ChunkReassembler.CleanExpired()` を定期的に呼ぶ。

```go
func (tb *TCPBridge) runJanitor(ctx context.Context) {
	ticker := time.NewTicker(chunkJanitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tb.reassembler.CleanExpired()   // 未配線だった漏れを塞ぐ
		}
	}
}
```

### ページ確保の上限検証（R5）

`vram.go` の `handleSetPageSize` に上限を追加する。

```go
const (
	MaxPageDimension = 4096
	MaxPagePixels    = 4096 * 4096
)

func (v *VRAMModule) handleSetPageSize(msg *bus.BusMessage) {
	c, err := arcproto.DecodeSetPageSize(msg.Data)
	if err != nil {
		return
	}
	w, h := int(c.W), int(c.H)
	if w <= 0 || h <= 0 || w > MaxPageDimension || h > MaxPageDimension || w*h > MaxPagePixels {
		v.publishPageError(errCodeInvalidPageSize)
		return
	}
	// 以降、従来の確保処理
}
```

`set_page_count` も同様に上限を設ける（現状 `count == 0` を 256 と解釈し、
256 ページ × 既定サイズを確保する。既定 256×212 なら約 55MB で許容範囲だが、
`set_page_size` と組み合わされた場合を考慮する）。

### ハンドシェイクの設計（R7）

| 種別 | Target | payload |
|---|---|---|
| コマンド | `get_capabilities` | なし（相関 ID で応答を識別） |
| イベント | `capabilities` | JSON（可読性と拡張性を優先） |

`get_stats` の応答が既に JSON である（`vram.go` 128-134 行の `publishStats`）ため、
同じ方式を採るのが一貫している。

```json
{
  "protocol_version": 1,
  "screen": { "width": 256, "height": 212 },
  "pages":  { "max_count": 256, "max_width": 4096, "max_height": 4096 },
  "palette": { "entries": 256, "channels": 4 },
  "commands": ["mode", "draw_pixel", "blit_rect", "batch", "..."],
  "features": ["batch", "vsync", "input", "chunking"]
}
```

応答を発行するモジュールは、画面サイズを知っている VRAM が担うのが自然である。

### 変更対象ファイル

| ファイル | 変更内容 |
|---|---|
| `features/neurom/internal/bus/message.go` | `Encode` / `Decode` をバイナリレイアウトへ置換 |
| `features/neurom/internal/bus/tcpbridge.go` | 受信ループ、janitor、チャンク配線、トピック統一、上限検証、統計 |
| `features/neurom/internal/bus/message_test.go` | 新レイアウトのテストへ改訂 |
| `features/neurom/internal/bus/tcpbridge_test.go` | 双方向のテスト追加 |
| `features/neurom/internal/bus/zmqbus.go` | R13 の判断に従い削除または存置 |
| `features/neurom/internal/modules/vram/vram.go` | ページ確保の上限検証、`get_capabilities` 応答 |
| `features/neurom/arcproto/` | `get_capabilities` / `capabilities` の定義、上限定数 |
| `features/neurom/cmd/main.go` | `--tcp-cmd-port` フラグ、`TCPBridgeConfig` への受信設定 |
| `features/neurom/internal/statsserver/server.go` | 外部接続統計の出力（R12） |
| `prompts/specifications/VRAM-Specification.md` | R10 の追記 |

### 本仕様で扱わないこと（スコープ外）

- `arc.Dial`（SDK の TCP 接続） → 029
- `features/metov/` の作成と別バイナリ化 → 029
- 認証・認可・暗号化（R8 で「行わない」ことを明記するのみ）
- 分散実行（複数マシンでのモジュール分散）
- 多言語 SDK の本格実装（R11 で最小の接続確認スクリプトのみ）

## 検証シナリオ (Verification Scenarios)

1. `message.go` の `Encode` / `Decode` をバイナリレイアウトへ置換し、ラウンドトリップ単体テストを通す
2. エンベロープのゴールデンバイト列テストを追加し、仕様書のレイアウトと一致することを確認する
3. 相関 ID がエンベロープを往復して保持されることを確認する
4. `TCPBridge` に PULL 受信ループを追加し、`--tcp-cmd-port` フラグを実装する
5. 外部から `clear_vram` を送信し、VRAM の内容が変化することを確認する（**双方向化の中核検証**）
6. 外部から `blit_rect` を送信し、Monitor 側の描画に反映されることを確認する
7. 外部から `read_rect` を相関 ID 付きで送信し、PUB 経路で応答が返ることを確認する
8. 送信側のチャンク分割を配線し、262KB の `blit_rect`（512×512）が分割送出されることを確認する
9. 受信側の再組立を配線し、分割されたメッセージが正しく復元されることを確認する
10. チャンクを意図的に 1 個欠落させ、タイムアウト後に破棄されメモリが解放されることを確認する（janitor の動作確認）
11. トピックフレームに内部トピック（`evt.vram` 等）が入ることを確認し、外部から接頭辞購読ができることを確認する
12. `w = h = 65535` の `set_page_size` を外部から送信し、**プロセスが死なずエラーイベントが返る**ことを確認する
13. 不正なエンベロープ（magic 不一致、長さ矛盾、切り詰め）を外部から送信し、panic せず破棄されることを確認する
14. 上限超過サイズのメッセージが拒否され、統計に計上されることを確認する
15. `get_capabilities` を外部から送信し、画面サイズ・ページ上限・対応コマンド一覧が返ることを確認する
16. 受信ソケットで panic を誘発させ、送信経路が停止しないことを確認する
17. `--no-tcp` 指定時に送信・受信の両方が起動しないことを確認する
18. ポート再利用（連続再起動）で起動時 panic が発生しないことを確認する
19. 既存の TCPBridge テストおよび panic guard テストが全件 PASS することを確認する
20. Python 等の最小クライアントスクリプトで接続・コマンド送信・イベント受信ができることを確認する
21. `ZMQBus` の扱い（削除 / 存置）を決定し、`023` の記述との齟齬を解消する
22. プロトコル仕様書にエンベロープ・チャンク・トピック・ポート・ハンドシェイク・上限・信頼モデルを追記する
23. `scripts/process/build.sh` を実行し、全ビルドと単体テストが PASS することを確認する

## テスト項目 (Testing for the Requirements)

| 要件 | 検証方法 | コマンド / 手段 |
|---|---|---|
| R1: 受信経路 | 外部から送った `clear_vram` が VRAM に反映される | `cd features/neurom && go test -v -count=1 -run "TestTCPBridgeInbound" ./internal/bus/...` |
| R1: 双方向同時 | 送信と受信が同時に機能する | `cd features/neurom && go test -v -count=1 -run "TestTCPBridgeBidirectional" ./integration/...` |
| R1: 片側障害の分離 | 受信側 panic で送信が継続する | `cd features/neurom && go test -v -count=1 -run "TestTCPBridgeReceiverPanicIsolation" ./internal/bus/...` |
| R2: エンベロープ | ラウンドトリップとゴールデンバイト列 PASS | `cd features/neurom && go test -v -count=1 -run "TestEnvelope" ./internal/bus/...` |
| R2: gob 不使用 | `encoding/gob` の import が 0 件 | `grep -rn "encoding/gob" features/neurom/`（0 件） |
| R2: 相関 ID の往復 | ID がエンベロープを越えて保持される | `cd features/neurom && go test -v -count=1 -run "TestEnvelopeRequestID" ./internal/bus/...` |
| R3: トピック統一 | トピックフレームが内部トピックと一致 | `cd features/neurom && go test -v -count=1 -run "TestTCPBridgeTopic" ./internal/bus/...` |
| R3: 接頭辞購読 | 外部から `evt.` で全イベントを購読できる | `cd features/neurom && go test -v -count=1 -run "TestExternalPrefixSubscribe" ./integration/...` |
| R4: 分割送出 | 262KB メッセージが複数フレームで送られる | `cd features/neurom && go test -v -count=1 -run "TestTCPBridgeChunkSend" ./internal/bus/...` |
| R4: 再組立 | 分割メッセージが正しく復元される | `cd features/neurom && go test -v -count=1 -run "TestTCPBridgeChunkReassemble" ./internal/bus/...` |
| R4: janitor | 欠落チャンクがタイムアウトで解放される | `cd features/neurom && go test -v -count=1 -run "TestChunkJanitor" ./internal/bus/...` |
| R5: サイズ上限 | 上限超過が拒否され統計に計上される | `cd features/neurom && go test -v -count=1 -run "TestInboundSizeLimit" ./internal/bus/...` |
| R5: ページ上限 | `w=h=65535` でプロセスが死なずエラーが返る | `cd features/neurom && go test -v -count=1 -run "TestSetPageSizeLimit" ./integration/...` |
| R5: 不正入力耐性 | 各種の壊れたエンベロープで panic しない | `cd features/neurom && go test -v -count=1 -run "TestInboundMalformed" ./internal/bus/...` |
| R5: ファジング | ランダムバイト列で panic しない | `cd features/neurom && go test -v -count=1 -fuzz "FuzzDecodeEnvelope" -fuzztime 60s ./internal/bus/...` |
| R6: 隔離特性 | 既存の panic guard テストが全件 PASS | `scripts/process/integration_test.sh --specify "TestBusPanicGuard\|TestTCPBridge"` |
| R6: 起動時 panic なし | 連続再起動で panic しない | `cd features/neurom && go test -v -count=5 -run "TestTCPBridgeRestart" ./internal/bus/...` |
| R7: ハンドシェイク | 能力照会の応答内容が実装と一致 | `cd features/neurom && go test -v -count=1 -run "TestGetCapabilities" ./integration/...` |
| R7: ハードコード排除 | 画面サイズ定数のコピーが残っていない | `grep -rn "VRAMWidth\s*=\|VRAMHeight\s*=" features/neurom/`（`internal/modules/vram` 内の定義のみ） |
| R8: 既定ループバック | 既定バインドが `127.0.0.1` である | `cd features/neurom && go test -v -count=1 -run "TestDefaultBindLoopback" ./internal/bus/...` |
| R9: CLI 互換 | `--no-tcp` で送信・受信とも起動しない | `cd features/neurom && go test -v -count=1 -run "TestNoTCPFlag" ./integration/...` |
| R10: 仕様書更新 | 記載項目の網羅を目視確認 | `prompts/specifications/VRAM-Specification.md` のレビュー |
| R11: 参照クライアント | 外部スクリプトで接続・送信・受信ができる | `./bin/neurom.exe --headless` 起動後にスクリプトを実行 |
| R12: 外部接続統計 | `/stats` に受信数・拒否数が現れる | `./bin/neurom.exe --headless --stats-port 8080` + `cd features/stats && go run . --endpoint http://127.0.0.1:8080/stats` |
| R13: `ZMQBus` の扱い | 削除の場合は参照 0 件、存置の場合は理由が文書化されている | `grep -rn "NewZMQBus" features/neurom/` |
| 全体リグレッション | ビルド + 全単体テスト | `scripts/process/build.sh` |

### 補足: 検証スクリプトの現状

`scripts/process/build.sh` は各 feature で `go list ./... | grep -v '/tests/'` を実行するため、
`features/neurom/integration/` も単体テストとして実行される。実質的な全体検証ゲートは `build.sh` である。
`scripts/process/integration_test.sh` はリポジトリルートの `tests/go.mod` を前提とするが、
現状 `tests/` は存在しないため warn を出して exit 0 する（no-op）。
上表の `integration_test.sh --specify` は統合テストが `tests/` へ移設された後に有効となり、
それまでは `build.sh` および `cd features/neurom && go test -run ... ./integration/...` で代替する。

## 対応ステータス

- **ステータス**: 未着手
- **実装計画**: 未作成
