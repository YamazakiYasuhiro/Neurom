# 029: 拡張ソフトウェア Metov の別プロセス分離

## 背景 (Background)

### 設計思想と実装の乖離

`prompts/specifications/ARC-Architecture.md` 29-35 行は、基盤と拡張ソフトウェアの分離を宣言している。

> ### Neurom (ノイロム)
> **Neurom**は、上記のARC Architecture規格に準拠して実装された**モジュール群の集合体・エミュレータ本体**の名称です。
>
> ### Metov (メトフ)
> **Metov**は、Neuromというプラットフォーム（仮想ハードウェア環境）の能力を示すために用意された
> **Neurom付属のサンプルプログラム（ゲーム）** の名称です。ユーザーは、Neurom上で動作するソフトウェアの一例として
> Metovをプレイしながら内部パラメータをハックしたり、全く新しい自作のソフトを構築してNeurom上で動かすことができます。

しかし **Metov のディレクトリ・コード・実装計画は一切存在しない**。
Metov への言及はこの 1 段落と `features/README.md` 25 行の一文だけである。

現実には、デモプログラムが基盤の内部モジュールとして同居している。
`features/neurom/internal/modules/cpu/cpu.go` は 548 行のうち約 300 行が
デモシーン（`scene1`〜`scene7`）であり、**「アプリ」と「基盤」が同一パッケージ・同一バイナリ**である。

024〜028 を経てこの分離の下地は整うが、
**最後の一歩（別プロセス・別バイナリ化）が残る**のが本仕様の対象である。

### `cpu` モジュールという名前の問題

ARC Architecture は構成要素として「CPUモジュール: 命令のフェッチと実行を担当」を挙げている
（`ARC-Architecture.md` 14 行）。

しかし 027 完了時点で `cpu` モジュールに残るのは
「デモを起動するだけの薄いホスト」であり、命令の実行主体ではない。
**実際に「命令をフェッチして実行する」のは拡張ソフトウェアそのものである。**

つまり ARC の言う CPU の役割は、バス越しに接続してくる拡張ソフトウェアが担う。
Neurom が提供すべきは VRAM / IO / Monitor / BUS であり、CPU は本来 Neurom の内側に無い。
この整理を本仕様で確定させる。

### 追い風: ビルド規約がすでに対応している

`scripts/process/build.sh` 96-140 行は `features/*/` を走査し、
`go.mod` を持つディレクトリごとに `bin/{feature_name}` へビルドする。

```bash
for feature_dir in features/*/; do
    if [[ ! -f "$feature_dir/go.mod" ]]; then
        info "Skipping $feature_dir — no go.mod found."
        continue
    fi
    # ... go test → go build -o "$PROJECT_ROOT/bin/${feature_name}${ext}" ./cmd/...
done
```

したがって `features/metov/go.mod` を作れば **`bin/metov.exe` が自動でビルドされる**。
既存の `features/stats` が同じ構成の前例である。

### 既知の技術的制約: 可視性とパッケージ依存

`arc.Embed(b bus.Bus)` のように **公開 API のシグネチャに `internal/bus` の型が現れる**と、
外部モジュール（`features/metov`）はその関数を呼び出せない
（Go の `internal` ルールにより `internal/bus` を import して引数を作れない）。

`Embed` は in-process 専用なので外部から呼べなくても問題ないが、
`arc` パッケージ全体が `internal/bus` に依存する構成にしておくと、
公開 API と内部専用 API の境界が曖昧になる。
本仕様でパッケージ分割を整理する（027 の構成に対する調整）。

### 本仕様の位置づけ

拡張ソフトウェア分離（024〜029）の**最終ステップ**。
これが完了すると「拡張ソフトウェア + API → BUS → VRAM」という当初の目標構造が実現する。

```
┌────────────────────────────────────┐
│ features/metov/  (bin/metov.exe)   │  拡張ソフトウェア
│   github.com/axsh/metov            │
└─────────────┬──────────────────────┘
              │ import
              ▼
┌────────────────────────────────────┐
│ github.com/axsh/neurom/arc         │  SDK（公開）
│ github.com/axsh/neurom/arcproto    │  プロトコル（公開）
└─────────────┬──────────────────────┘
              │ TCP (ZeroMQ)
              ▼
┌────────────────────────────────────┐
│ features/neurom/ (bin/neurom.exe)  │  基盤
│   BUS / VRAM / IO / Monitor        │
└────────────────────────────────────┘
```

- 前提: [024](file://prompts/phases/000-foundation/branches/main/ideas/024-ARCProtocol-PackageExtraction.md), [025](file://prompts/phases/000-foundation/branches/main/ideas/025-BusProtocol-BatchAndAddressing.md), [026](file://prompts/phases/000-foundation/branches/main/ideas/026-PlatformIO-InputAndFrameClock.md), [027](file://prompts/phases/000-foundation/branches/main/ideas/027-ARCSdk-EmbeddedFrameAPI.md), [028](file://prompts/phases/000-foundation/branches/main/ideas/028-ExternalBus-BidirectionalTransport.md)

## 要件 (Requirements)

### 必須要件

1. **R1: SDK パッケージの依存整理**
   - `arc` の公開 API シグネチャに `internal/` 配下の型が現れないようにする。
   - in-process 接続（`Embed`）は別サブパッケージへ分離し、
     `internal/bus` への依存をそこに閉じ込める。
   - 拡張ソフトウェアが import するパッケージの依存に
     **GUI 関連（`golang.org/x/mobile`）が含まれないこと**
     （拡張ソフトウェアはウィンドウを持たないため）。
   - 接続方式を抽象化する型（トランスポート）を `arc` に定義する。

2. **R2: TCP 接続 API（`Dial`）**
   - エンドポイントを指定して外部プロセスから接続する関数を提供する。
   - 028 で追加した送信（PUB）・受信（PULL）の 2 ポートを扱うこと。
   - 接続確立時に 028 の能力照会を実行し、画面サイズ等を `Device` に反映すること。
   - **`App` の実装コードが接続方式に依存しないこと。**
     `Embed` から `Dial` へ差し替えても `Init` / `Update` を 1 行も変更せずに動くこと。

3. **R3: 接続ライフサイクルの定義**
   - 以下の各状況における振る舞いを定義し、実装する。
     - 接続先の Neurom が起動していない → 明確なエラーで終了（無言のハングを禁止）
     - 接続中に Neurom が shutdown した → 拡張ソフトウェアも正常終了
     - 接続中に Neurom が異常終了した → タイムアウト検知して終了
     - 拡張ソフトウェアが先に終了した → Neurom は動作を継続する
   - 接続確立のタイムアウトと、vsync 途絶の検知タイムアウトを設けること。

4. **R4: `features/metov/` の新設**
   - 独立した Go モジュールとして作成する。
     モジュールパスは `github.com/axsh/metov`。
   - `features/neurom` をローカル参照するための `replace` ディレクティブを置く。
   - ディレクトリ構成は `features/README.md` の規約（`cmd/`, `internal/`, `go.mod`）に従う。
   - `scripts/process/build.sh` により `bin/metov.exe` が自動生成されること
     （build.sh 自体の変更は不要であることを確認する）。

5. **R5: デモの移設**
   - 027 で `features/neurom/internal/demo/` に置いたデモ一式を
     `features/metov/` へ移設する。
   - 移設後、`features/neurom` 側にデモコードが**一切残らない**こと。
   - 移設によってシーンの見た目が変わらないこと。

6. **R6: `cpu` モジュールの廃止**
   - `features/neurom/internal/modules/cpu/` を削除する。
   - 命令実行主体（ARC の言う CPU）は**バス越しに接続する拡張ソフトウェア**が担う、
     という整理を `ARC-Architecture.md` に反映する。
   - `cmd/main.go` の `mgr.Register(cpu.New())` を削除する。
   - `cpu_test.go` は廃止するか、必要な検証を他のテストへ移す。

7. **R7: 拡張ソフトウェア無しでの基盤の振る舞い**
   - Neurom 単体起動時に**黒画面で無反応にならない**こと。
     現状はデモが常に動いているため、これは実質的な体験の後退になる。
   - 起動時に基盤自身が最小の起動画面（ロゴ、または接続待ちを示す表示）を描画する。
     実機のゲーム機が「カートリッジ未挿入」を表示するのと同じ位置づけ。
   - この起動画面は基盤側の責務とし、拡張ソフトウェアが接続してきたら
     その描画に上書きされて構わない（明示的な排他制御は行わない）。
   - 起動画面の実装は**極小**に留め、シーン管理やアニメーション基盤を持ち込まないこと。

8. **R8: 起動手順のドキュメント化**
   - 2 プロセスの起動手順を `features/README.md` に記載する。
   - 記載すべき内容:
     - Neurom を起動する（TCP 有効が前提。`--no-tcp` では拡張ソフトが繋がらないこと）
     - Metov を起動して接続する
     - ポート指定の対応関係
     - 起動順序の制約（R3 の挙動）
   - `features/README.md` の既存の陳腐化も併せて修正する。
     現状は見出しが `### vm` のままでディレクトリ名 `neurom` と一致せず、
     実行例も `cd features/vm` になっている（実在しないパス）。

9. **R9: 分離の検証可能性**
   - 「基盤に拡張ソフトのコードが残っていない」ことを機械的に検証できること。
     - `features/neurom/` 配下にシーン名・スプライト定義が存在しないこと
     - `features/neurom/` が `features/metov/` を参照していないこと（依存が一方向であること）
   - 「拡張ソフトが低水準要素を触っていない」ことを機械的に検証できること。
     - `features/metov/` に `internal/bus` の import が無いこと
     - `features/metov/` に `binary.BigEndian` が現れないこと

10. **R10: ヘッドレスでの結合テスト**
    - Neurom と Metov の 2 プロセスを起動し、
      実際に描画コマンドが届いていることを自動検証する手段を用意する。
    - 検証手段の候補: `/stats` のコマンド計上を確認する、
      または Neurom 側に矩形読み出しで内容を確認するテスト用クライアントを併走させる。
    - CI で実行可能な形（タイムアウト付き・GUI 不要）にすること。

### 任意要件

11. **R11: 基盤による拡張ソフトの起動（ランチャ）**
    - Neurom に拡張ソフトウェアのパスを渡すオプションを追加し、
      子プロセスとして起動・監視・停止まで面倒を見る。
    - 「カートリッジを挿す」体験に相当し、利用者が 2 つのコマンドを
      手で打つ必要がなくなる。
    - 本仕様では**任意**とし、まずは手動 2 段起動（R8）を正式手順とする。

12. **R12: 自動再接続**
    - Neurom が再起動した場合に拡張ソフトウェア側が再接続を試みる。
    - 開発中の反復を速くする効果がある。

13. **R13: 多言語参照実装**
    - Python 等で最小の拡張ソフトウェア（1 スプライトを動かす程度）を実装し、
      `ARC-Architecture.md` 26 行が謳う「多言語対応」を実証する。
    - 028 の R11（接続確認スクリプト）を発展させたものと位置づける。

14. **R14: 拡張ソフトウェア作成ガイド**
    - 「Neurom 上で動くソフトを自作する」ための入門ドキュメント。
    - 最小の `App` 実装から始めて、スプライト描画・入力・ページ切替までを段階的に示す。
    - `ARC-Architecture.md` が謳う hackability の入口となる。

15. **R15: `shared/libs/` の活用判断**
    - `shared/libs/` は README のみで空であり、cross-feature 共有の置き場として予約されている。
    - `arcproto` を将来ここへ移すか、`features/neurom` 配下に留めるかを判断し、記録する。
    - 現時点では `replace` による参照で足りるため、移設は不要と考えられる。

## 実現方針 (Implementation Approach)

### SDK のパッケージ分割（R1）

027 の構成を以下のように調整する。

```
features/neurom/
  arcproto/            公開。プロトコル定義。依存は stdlib のみ
  arc/                 公開。SDK 本体
    transport.go         Transport インタフェース定義
    dial.go              TCP トランスポート（zmq4 に依存）
    device.go, frame.go, ...
  arc/embed/           公開だが in-process 専用。internal/bus に依存
    embed.go             New(b bus.Bus) arc.Transport
  internal/            従来どおり非公開
```

`Transport` を境界にすることで、`arc` 本体は `internal/bus` に依存しなくなる。

```go
package arc

// Transport はバスとの送受信を抽象化する。
type Transport interface {
	Send(topic string, msg Message) error
	Recv() <-chan Message
	Close() error
}

// Message は SDK 側で見えるメッセージ。internal/bus の型を露出しない。
type Message struct {
	Topic     string
	Target    string
	Operation uint8
	RequestID uint32
	Data      []byte
}

func New(t Transport) (*Device, error)
func Dial(cfg DialConfig) (*Device, error)   // 内部で TCP Transport を構築
```

in-process 側は薄いアダプタになる。

```go
package embed

// New は既存の in-process バスを arc.Transport として包む。
func New(b bus.Bus) (arc.Transport, error)
```

これにより、
- `features/metov` は `arc` と `arcproto` だけを import する
- `features/neurom` 内のテストは `arc/embed` を使って in-process で高速に回せる
- 拡張ソフトウェアの依存に `golang.org/x/mobile` が入らない

`internal/bus` は `zmq4` に依存するが `golang.org/x/mobile` には依存しない
（GUI 依存は `internal/modules/monitor` のみ）ため、
`arc/embed` 経由でも GUI が引き込まれることはない。
とはいえ公開境界を明確にする意味で分割する価値がある。

### 接続の設定

```go
type DialConfig struct {
	// イベント購読先（Neurom の PUB）。既定 tcp://127.0.0.1:5555
	EventEndpoint string
	// コマンド送信先（Neurom の PULL）。既定 tcp://127.0.0.1:5556
	CommandEndpoint string
	// 接続確立のタイムアウト
	ConnectTimeout time.Duration
	// vsync 途絶を異常と判定する時間
	VsyncTimeout time.Duration
}
```

`Dial` は接続後に 028 の能力照会を実行し、
画面サイズ・ページ上限を `Device` に保持する。
拡張ソフトウェアは `d.ScreenWidth()` / `d.ScreenHeight()` で参照でき、
`VRAMWidth = 256` のようなハードコードが不要になる。

### 接続ライフサイクル（R3）

| 状況 | 検知方法 | 振る舞い |
|---|---|---|
| Neurom 未起動 | 能力照会が `ConnectTimeout` 内に応答しない | エラーを返して終了。「Neurom が起動していない可能性」を示すメッセージ |
| Neurom が shutdown | `sys` トピックの shutdown メッセージ受信 | `Run` が正常終了（`nil` を返す） |
| Neurom が異常終了 | vsync が `VsyncTimeout` 途絶 | エラーを返して終了 |
| 拡張ソフトが先に終了 | — | Neurom は影響を受けず継続（028 の PULL は接続断を検知しないため自然に成立） |

PUSH/PULL は接続が確立していなくても送信側がバッファリングするため、
**「送れているつもりで届いていない」状態が起こり得る**。
能力照会による往復確認を接続確立の判定に用いるのが重要である。

### `features/metov/` の構成

```
features/metov/
  go.mod
  go.sum
  cmd/
    main.go          // Dial して Run するだけ
  internal/
    game/
      game.go        // App 実装。シーンのローテート
      scenes.go      // 7 シーン
      sprites.go     // スプライト定義
```

`go.mod`:

```
module github.com/axsh/metov

go 1.25.0

require github.com/axsh/neurom v0.0.0

replace github.com/axsh/neurom => ../neurom
```

`cmd/main.go`:

```go
package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"github.com/axsh/neurom/arc"
	"github.com/axsh/metov/internal/game"
)

func main() {
	eventEP := flag.String("event-endpoint", "tcp://127.0.0.1:5555", "Neurom event endpoint")
	cmdEP := flag.String("command-endpoint", "tcp://127.0.0.1:5556", "Neurom command endpoint")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dev, err := arc.Dial(arc.DialConfig{
		EventEndpoint:   *eventEP,
		CommandEndpoint: *cmdEP,
	})
	if err != nil {
		log.Fatalf("failed to connect to Neurom: %v", err)
	}
	defer dev.Close()

	if err := arc.Run(ctx, dev, game.New()); err != nil {
		log.Fatalf("metov exited with error: %v", err)
	}
}
```

**これが拡張ソフトウェアの全体像**である。基盤の内部構造は一切現れない。

### 基盤側の起動画面（R7）

`cpu` モジュールを削除すると、拡張ソフトウェア未接続時に何も描かれなくなる。
実機のゲーム機に倣い、基盤自身が最小の起動画面を出す。

置き場所は `features/neurom/internal/modules/boot/` を想定する。

- Monitor / VRAM と同じ `Module` インタフェースを実装する
- 起動時に一度だけ、パレットと簡単な図形（またはロゴのビットマップ）を描画する
- **フレームループを持たない**（アニメーションしない）
- 拡張ソフトウェアが描き始めたら自然に上書きされる

これにより、Neurom 単体起動が「動いているのか分からない黒画面」にならず、
かつ基盤にアプリのロジックが戻ってくることもない。

`internal/demo/` のシーン管理機構をここへ流用してはならない（R7 の「極小に留める」）。

### `ARC-Architecture.md` の更新（R6）

14 行の構成要素一覧に対し、CPU の位置づけを明確化する追記を行う。

- Neurom が内包するモジュール: VRAM / IO / Monitor / BUS
- CPU の役割（命令のフェッチと実行）は、バス越しに接続する拡張ソフトウェアが担う
- Metov はその実装例であり、`bin/metov.exe` として独立したプロセスで動作する

### 変更・新規対象ファイル

| ファイル | 変更内容 |
|---|---|
| `features/neurom/arc/transport.go` | 新規。`Transport` / `Message` 定義 |
| `features/neurom/arc/dial.go` | 新規。TCP トランスポートと `Dial` |
| `features/neurom/arc/embed/embed.go` | 新規。in-process アダプタ（027 の `Embed` を移設） |
| `features/neurom/arc/device.go` | 能力照会の反映、画面サイズ参照 API |
| `features/neurom/internal/modules/cpu/` | **削除** |
| `features/neurom/internal/demo/` | **削除**（`features/metov/` へ移設） |
| `features/neurom/internal/modules/boot/` | 新規。最小の起動画面 |
| `features/neurom/cmd/main.go` | `cpu` の登録を削除、`boot` の登録を追加 |
| `features/neurom/integration/` | `arc/embed` を用いたテストへの追随、2 プロセス結合テスト |
| `features/metov/` | 新規。モジュール一式 |
| `features/README.md` | Metov の追記、2 プロセス起動手順、`vm` → `neurom` の陳腐化修正 |
| `prompts/specifications/ARC-Architecture.md` | CPU の位置づけの明確化 |

### 本仕様で扱わないこと（スコープ外）

- ROM / カートリッジ形式の定義（バイナリをロードして供給する仕組み）
  `ideas/000-RetroGameBus-ModularArchitecture.md` 184 行に
  「ゲームROM読み込み」が今後の拡張として 1 行あるが、本仕様では扱わない。
  拡張ソフトウェアは独立した実行可能バイナリである。
- サウンド（APU）
- 認証・認可（028 の R8 で「行わない」と明記済み）
- 分散実行（別マシンでの動作。`Dial` のエンドポイント指定で技術的には可能だが検証しない）
- 拡張ソフトウェアの複数同時接続時の調停

## 検証シナリオ (Verification Scenarios)

1. `arc` に `Transport` / `Message` を定義し、027 の `Embed` を `arc/embed` サブパッケージへ移す
2. `arc` 本体が `internal/` 配下に依存していないことを確認する
3. `arc` の依存に `golang.org/x/mobile` が含まれないことを確認する
4. `arc.Dial` を実装し、028 の PUB / PULL 2 ポートに接続できることを確認する
5. `Dial` 時に能力照会が実行され、画面サイズが `Device` に反映されることを確認する
6. Neurom を起動せずに `Dial` を呼び、タイムアウトして明確なエラーで終了することを確認する（無言のハングをしないこと）
7. 接続中に Neurom を shutdown させ、拡張ソフトウェアが正常終了（エラーなし）することを確認する
8. 接続中に Neurom を強制終了させ、vsync 途絶を検知してエラー終了することを確認する
9. 拡張ソフトウェアを先に終了させ、Neurom が影響を受けず動作を継続することを確認する
10. `features/metov/` を作成し、`go.mod` に `replace` を設定する
11. `scripts/process/build.sh` を実行し、**build.sh を変更せずに** `bin/metov.exe` が生成されることを確認する
12. `internal/demo/` のデモ一式を `features/metov/internal/game/` へ移設する
13. `internal/modules/cpu/` を削除し、`cmd/main.go` の登録を外す
14. `internal/modules/boot/` に最小の起動画面を実装し、`cmd/main.go` に登録する
15. Neurom を単体起動し、**黒画面ではなく起動画面が表示される**ことを目視確認する
16. Neurom を起動した状態で Metov を起動し、**7 シーンが移設前と同じ見た目で表示される**ことを目視確認する
17. Metov で矢印キー等の入力が効くことを確認する（Monitor がウィンドウを持つため、入力は Neurom 側で捕捉され Metov へ届く経路になる）
18. `--no-tcp` で Neurom を起動し、Metov が接続できずエラー終了することを確認する
19. `features/neurom/` 配下にシーン名・スプライト定義が残っていないことを確認する
20. `features/metov/` に `internal/bus` の import と `binary.BigEndian` が無いことを確認する
21. `features/neurom` が `features/metov` に依存していないこと（一方向依存）を確認する
22. ヘッドレスで Neurom と Metov の 2 プロセスを起動し、`/stats` に描画コマンドが計上されることを自動検証する
23. `features/README.md` に Metov の説明と 2 プロセス起動手順を追記し、`vm` → `neurom` の陳腐化を修正する
24. `ARC-Architecture.md` に CPU の位置づけを追記する
25. `scripts/process/build.sh` を実行し、`neurom` と `metov` の両方でビルドと単体テストが PASS することを確認する

## テスト項目 (Testing for the Requirements)

| 要件 | 検証方法 | コマンド / 手段 |
|---|---|---|
| R1: 依存整理（internal） | `arc` が `internal/` に依存しない | `cd features/neurom && go list -deps ./arc \| grep "neurom/internal"`（0 件） |
| R1: 依存整理（GUI） | `arc` が `x/mobile` に依存しない | `cd features/neurom && go list -deps ./arc \| grep "golang.org/x/mobile"`（0 件） |
| R1: embed の分離 | `arc/embed` のみが `internal/bus` を参照 | `cd features/neurom && go list -deps ./arc/embed \| grep "neurom/internal/bus"`（1 件以上） |
| R2: TCP 接続 | 外部プロセスから接続して描画が届く | `cd features/metov && go test -v -count=1 -run "TestDialAndDraw" ./...` |
| R2: App の接続非依存 | 同じ `App` が `embed` と `Dial` の両方で動く | `cd features/neurom && go test -v -count=1 -run "TestAppTransportAgnostic" ./integration/...` |
| R2: 能力照会の反映 | `Device` の画面サイズが基盤の値と一致 | `cd features/metov && go test -v -count=1 -run "TestCapabilities" ./...` |
| R3: 未起動時 | タイムアウトして明確なエラー（ハングしない） | `cd features/metov && go test -v -count=1 -timeout 30s -run "TestDialNoServer" ./...` |
| R3: shutdown 連動 | Neurom の shutdown で正常終了 | `cd features/neurom && go test -v -count=1 -run "TestAppShutdownPropagation" ./integration/...` |
| R3: 異常終了検知 | vsync 途絶でエラー終了 | `cd features/metov && go test -v -count=1 -run "TestVsyncTimeout" ./...` |
| R3: 逆方向の独立 | 拡張ソフト終了後も基盤が継続 | `cd features/neurom && go test -v -count=1 -run "TestPlatformSurvivesAppExit" ./integration/...` |
| R4: 自動ビルド | build.sh 無変更で `bin/metov.exe` が生成される | `scripts/process/build.sh` 実行後に `ls bin/metov*` |
| R4: モジュール構成 | `replace` で `neurom` を解決できる | `cd features/metov && go build ./...` |
| R5: 移設の完全性 | 基盤側にデモコードが残っていない | `grep -rn "scene1\|scene7\|diamondSprite\|hsvToRGB" features/neurom/`（0 件） |
| R5: 見た目の維持 | 7 シーンが移設前と同じ | Neurom + Metov を起動し 21 秒以上目視 |
| R6: cpu 廃止 | `cpu` パッケージが存在せず参照も無い | `ls features/neurom/internal/modules/`（`cpu` が無いこと）+ `grep -rn "modules/cpu" features/`（0 件） |
| R7: 起動画面 | 単体起動で黒画面にならない | `./bin/neurom.exe` を起動して目視 |
| R7: 起動画面の極小性 | `boot` モジュールにフレームループが無い | `features/neurom/internal/modules/boot/` のコードレビュー（`time.Ticker` / vsync 購読が無いこと） |
| R8: ドキュメント | 2 プロセス起動手順の記載と陳腐化修正 | `features/README.md` のレビュー（`cd features/vm` が残っていないこと） |
| R9: 一方向依存 | 基盤が拡張ソフトを参照しない | `grep -rn "axsh/metov" features/neurom/`（0 件） |
| R9: 低水準要素の排除 | 拡張ソフトが内部型を触らない | `grep -rn "internal/bus\|binary.BigEndian" features/metov/`（0 件） |
| R10: 2 プロセス結合 | ヘッドレスで描画コマンドが計上される | `./bin/neurom.exe --headless --stats-port 8080` + `./bin/metov.exe` + `cd features/stats && go run . --endpoint http://localhost:8080/stats` |
| R10: CI 実行可能性 | GUI 不要・タイムアウト付きで完走する | `cd features/neurom && go test -v -count=1 -timeout 120s -run "TestTwoProcess" ./integration/...` |
| R11: ランチャ | `--app` 指定で子プロセスが起動・停止する | `cd features/neurom && go test -v -count=1 -run "TestAppLauncher" ./integration/...` |
| R12: 自動再接続 | Neurom 再起動後に再接続する | `cd features/metov && go test -v -count=1 -run "TestReconnect" ./...` |
| R13: 多言語実装 | Python 実装でスプライトが動く | スクリプト実行 + 目視 |
| 全体リグレッション | 両 feature のビルド + 全単体テスト | `scripts/process/build.sh` |
| 停止処理リグレッション | 既存の停止テストが全件 PASS | `scripts/process/integration_test.sh --specify "TestGracefulShutdown\|TestShutdown\|TestAppMainShutdown"` |

### 補足: 検証スクリプトの現状

`scripts/process/build.sh` は `features/*/` を走査して `go.mod` を持つ各ディレクトリで
`go list ./... | grep -v '/tests/'` による単体テストと `go build -o bin/{feature}` を実行する。
本仕様により対象が `neurom` / `stats` / `metov` の 3 つになる。
`features/neurom/integration/` はパス名が `/tests/` に一致しないため `build.sh` の対象に含まれる。
実質的な全体検証ゲートは `build.sh` である。

`scripts/process/integration_test.sh` はリポジトリルートの `tests/go.mod` を前提とするが、
現状 `tests/` は存在しないため warn を出して exit 0 する（no-op）。
上表の `integration_test.sh --specify` は統合テストが `tests/` へ移設された後に有効となり、
それまでは `build.sh` および `cd features/neurom && go test -run ... ./integration/...` で代替する。

なお `build.sh` は `go build -ldflags "-s -w" -o bin/{feature} ./cmd/...` を実行するため、
1 feature に main パッケージは 1 つまでである。`features/metov/cmd/` に置く
main パッケージは 1 つに限ること。

## 対応ステータス

- **ステータス**: 未着手
- **実装計画**: 未作成
