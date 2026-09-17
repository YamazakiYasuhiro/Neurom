# 027: ARC SDK（拡張ソフトウェア向け API）と in-process 実行

## 背景 (Background)

### 現状の問題: 拡張ソフトウェアを書くための API が存在しない

024 で `arcproto` を抽出してもなお、拡張ソフトウェアの作者が書くコードは
**バイトレイアウトを意識した低水準のものになる**。
現状のデモコード（`features/neurom/internal/modules/cpu/cpu.go` 328-338 行）を見ると分かる。

```go
func scene3Update(c *CPUModule, b bus.Bus, frame int) {
	c.publishClearVRAM(b, 0, 0)
	rot := uint8((frame * 2) % 256)

	// Center: normal rotation
	c.publishBlitRectTransform(b, 0, 128, 106, 8, 8, 4, 4, rot, 0x0100, 0x0100, 0, diamondSprite)
	// Left: bottom pivot
	c.publishBlitRectTransform(b, 0, 64, 106, 8, 8, 4, 7, rot, 0x0100, 0x0100, 0, diamondSprite)
	// Right: 2x scale
	c.publishBlitRectTransform(b, 0, 192, 106, 8, 8, 4, 4, rot, 0x0200, 0x0200, 0, diamondSprite)
}
```

読みにくさの原因は 6 つあり、いずれも拡張ソフトウェアの作者が負う必要のないコストである。

| 原因 | 詳細 |
|---|---|
| バスの引き回し | `b bus.Bus` を全シーン関数の引数に渡している |
| 無意味なレシーバ | `c *CPUModule` はエンコードのためだけに存在し、状態は使っていない |
| 冗長な既定値 | 対象ページの `0` を毎回書いている |
| 魔法数 | `0x0100` が等倍、`0x0200` が 2 倍。コメントが無いと読めない |
| 独自単位 | 回転が 0-255 の生値（`(frame*2)%256`）。度数ではない |
| 位置引数 11 個 | `8, 8, 4, 4, rot, 0x0100, 0x0100, 0` が何を指すか呼び出し側から不明 |

さらに構造的な問題として、**1 コマンド = 1 メッセージのモデルは既に破綻している**。
`scene2Update`（`cpu.go` 278-296 行）は 1 tick で 864 回 `Publish` するが、
`ChannelBus` の購読バッファは 100 で、超過分は黙って破棄される（025 の背景を参照）。

### 現状の責務混在

`cpu.go` は 548 行あるが、その内訳は以下のとおりで、
**基盤としてのコードは実質 60 行しかない**。

| 行範囲 | 内容 | 本来の所属 |
|---|---|---|
| 1-60 | `CPUModule` のライフサイクル | 基盤 |
| 62-119 | `run` ループ（8ms ticker、3 秒でシーンローテート） | 拡張ソフト |
| 121-245 | `publish*` 系 12 関数 | API（024 で `arcproto` へ委譲済み） |
| 247-520 | `scene1`〜`scene7`、`diamondSprite` | 拡張ソフト |
| 522-547 | `hsvToRGB` | 拡張ソフト |

### 本仕様の位置づけ

拡張ソフトウェア分離（024〜029）の第 4 ステップ。
**「拡張ソフトウェアが書きやすい API」を確立し、デモをその API の上に載せ替える。**

本仕様の時点では**まだ単一バイナリのまま**（in-process）とし、
プロセス分離は 028 / 029 に委ねる。これにより API の設計と
トランスポートの変更を別々に検証できる。

- 前提: [024-ARCProtocol-PackageExtraction](file://prompts/phases/000-foundation/branches/main/ideas/024-ARCProtocol-PackageExtraction.md)（`arcproto`）
- 前提: [025-BusProtocol-BatchAndAddressing](file://prompts/phases/000-foundation/branches/main/ideas/025-BusProtocol-BatchAndAddressing.md)（`batch`、相関 ID）
- 前提: [026-PlatformIO-InputAndFrameClock](file://prompts/phases/000-foundation/branches/main/ideas/026-PlatformIO-InputAndFrameClock.md)（入力、vsync）
- 後続: 029（`arc.Dial` による out-of-process 化と Metov 分離）

## 要件 (Requirements)

### 必須要件

1. **R1: `arc` パッケージの新設**
   - `features/neurom/arc/` を作成する（**`internal/` の外**）。
   - モジュール外から `github.com/axsh/neurom/arc` として import 可能であること。
   - `arc` は `arcproto` に依存してよい。`internal/modules/*` に依存**してはならない**。

2. **R2: `App` インタフェースと実行ループ**
   - 拡張ソフトウェアが実装するインタフェースを定義する。
     初期化（1 回）と更新（フレームごと）の 2 メソッドを最小構成とする。
   - 実行ループ関数を提供し、vsync 購読・入力取得・フレーム flush・終了処理を
     すべて SDK 側が担うこと。**拡張ソフトウェアがバスやトピックを直接触る必要がないこと。**
   - 拡張ソフトウェアが終了処理を必要とする場合の任意インタフェースを用意する。

3. **R3: `Frame` によるコマンドバッファリング**
   - `Frame` は描画コマンドを蓄積するバッファとし、`Update` から戻った時点で
     **025 の `batch` コマンドとして 1 メッセージで送信**する。
   - 1 フレーム内で発行されたコマンド数に関わらず、バス上のメッセージは
     原則 1 件（バッチ）に収まること。
   - `Frame` オブジェクトはフレーム間で**再利用**し、毎フレームの確保を避けること。
   - コマンドが 0 件のフレームでは何も送信しないこと。

4. **R4: 人間可読な単位と名前付きオプション**
   - 回転を度数または回転数で指定できること（0-255 の生値を露出しない）。
   - 拡大率を `float64` で指定できること（`0x0100` を露出しない）。
   - ブレンドモードを名前付き定数で指定できること。
   - 変換パラメータは**可変長オプション**で与え、既定値（等倍・無回転・中心ピボット・
     置換ブレンド）を省略できること。

5. **R5: 変換なし描画の最適化**
   - 回転・拡大・ピボットのいずれも指定されない描画は、
     `blit_rect_transform` ではなく **`blit_rect` として発行**すること。
   - 変換処理は `internal/modules/vram/transform.go` を経由するため、
     不要な場合に通すとコストが無駄になる。

6. **R6: スプライトと画像の型付け**
   - 幅・高さ・ピクセル列を保持する型を定義し、`(w, h, []byte)` の三つ組の
     引き回しを廃止する。
   - 生成時に `len(pixels) == w * h` を検証すること。

7. **R7: 入力 API**
   - 026 の入力スナップショットを、`Held` / `Pressed` / `Released` の
     3 種の問い合わせとして提供する。
   - フレームごとの `Tick` に入力を同梱し、拡張ソフトウェアが
     イベント購読を意識せずに参照できること。

8. **R8: 時間 API**
   - `Tick` にフレーム連番、起動からの経過時間、前フレームからの差分時間を含める。
   - **フレーム数ベースではなく時間ベースでアニメーションを書けるようにする**
     （リフレッシュレートに依存しない拡張ソフトウェアを書けるようにするため）。

9. **R9: in-process 接続（`Embed`）**
   - 既存の `bus.Bus` に直結して `Device` を得る関数を提供する。
   - この時点では TCP 接続は提供しない（029 で追加）。
   - 拡張ソフトウェア側のコード（`App` の実装）は、接続方法に依存しないこと。
     **029 で接続方法を差し替えても `App` の実装を 1 行も変えずに済むこと。**

10. **R10: 初期化系 API（即時実行）**
    - パレット設定、ページ数設定、ページサイズ設定、表示ページ設定、ビューポート設定を
      `Device` のメソッドとして提供する。
    - これらはフレームバッファではなく即時送信でよい（`Init` から呼ばれる想定）。
    - ビューポート設定はフレームごとにも使うため `Frame` からも呼べること。

11. **R11: 同期読み出し API**
    - 025 の相関 ID を用いて、矩形読み出しとパレット読み出しを
      **同期的に値を返すメソッド**として提供する。
    - タイムアウトを設け、応答が無い場合はエラーを返すこと。

12. **R12: デモの SDK への載せ替え**
    - `scene1`〜`scene7` と `diamondSprite`、`hsvToRGB` を `cpu.go` から切り離し、
      `arc` SDK を用いた実装として書き直す。
    - 置き場所は `features/neurom/internal/demo/` とする
      （029 で `features/metov/` へ移設するため、この時点では `internal/` でよい）。
    - `cpu.go` は**デモを起動するだけの薄いホスト**に縮小する。
    - デモコード内に `bus.Bus` / `arcproto` / `binary.BigEndian` が
      **一切現れないこと**（SDK の十分性の検証）。

13. **R13: フレーム落ち時の方針**
    - `Update` の処理が 1 フレーム分の時間を超えた場合、
      滞留した vsync を消化して遅れを取り戻すのではなく、
      **古い vsync を破棄して最新フレームに追従する**（フレームスキップ）。
    - スキップしたフレーム数を診断できるようにすること。

14. **R14: デモの見た目に関する差異の明示**
    - 本仕様の変更により、デモの見た目は**意図的に変化する**。以下を許容差異として記録する。
      - **シーン 2（BlitRect）**: 現状は 864 コマンドのうち大半が破棄されているため
        画面が部分的にしか描かれていない。バッチ化により全コマンドが適用され、
        **画面全体が正しく敷き詰められるようになる**（不具合の解消）。
      - **アニメーション速度**: 更新レートが独自 8ms ticker（125Hz）から
        vsync（既定 60Hz）へ変わる。R8 の時間ベース化により
        **見かけの速度を現状と同等に保つ**こと。
    - 「現状と 1 ピクセルも同じ」ではなく「**意図した見た目になる**」を合格基準とする。

### 任意要件

15. **R15: パレット生成ユーティリティ**
    - `hsvToRGB` 相当の機能を SDK のユーティリティとして提供する
      （現状 `cpu.go` 524-547 行にあり、7 シーンのうち 4 つが使用している）。
    - HSV 環状ランプの生成など、レトロ表現で頻出するパレット生成を含める。

16. **R16: 図形描画ユーティリティ**
    - 単色矩形の塗りつぶしなど、`blit_rect` の頻出パターンを短く書ける補助を提供する。
    - 現状のデモは単色矩形を描くために毎回 `make([]byte, w*h)` してループで埋めている
      （`cpu.go` 370-374 行、385-388 行など）。

17. **R17: SDK のサンプルコード**
    - `arc` パッケージに Go の Example テスト（`ExampleRun` 等）を追加し、
      最小の拡張ソフトウェアの形を実行可能な形で示す。

18. **R18: 二重描画の検出**
    - 同一フレーム内で同一領域を複数回描画した場合に検出する診断モード。
      拡張ソフトウェア側の最適化を助ける。

## 実現方針 (Implementation Approach)

### パッケージ構成

```
features/neurom/arc/
  doc.go        // SDK の概要と最小サンプル
  device.go     // Device、Embed、初期化系 API、同期読み出し
  run.go        // App インタフェース、Run 実行ループ、フレームスキップ
  frame.go      // Frame（コマンドバッファ）、描画メソッド
  option.go     // DrawOption（Pivot / Rotate / Scale / Blend / Page）
  sprite.go     // Sprite 型
  input.go      // Input、Tick
  palette.go    // パレット生成ユーティリティ（R15）
  shape.go      // 図形ユーティリティ（R16）
```

### 公開 API の形

```go
package arc

// App は拡張ソフトウェアが実装するインタフェース。
type App interface {
	// Init は起動時に 1 回だけ呼ばれる。
	Init(d *Device) error
	// Update は vsync ごとに呼ばれる。f に描いて return すればまとめて送信される。
	Update(f *Frame, t Tick)
}

// Closer は終了処理が必要な App が任意で実装する。
type Closer interface {
	Close() error
}

// Embed は既存の in-process バスに直結した Device を返す。
func Embed(b bus.Bus) (*Device, error)

// Run は App を vsync に同期して実行する。ctx のキャンセルまたは
// システムの shutdown で戻る。
func Run(ctx context.Context, d *Device, app App) error

// Tick は 1 フレーム分の時間と入力の情報。
type Tick struct {
	Frame   uint64        // フレーム連番
	Elapsed time.Duration // 起動からの経過時間
	Delta   time.Duration // 前フレームからの差分
	Skipped int           // 直前に破棄した vsync 数（R13）
	Input   Input
}

// Input はフレーム境界での入力スナップショット。
type Input struct{ held, pressed, released arcproto.Button }

func (i Input) Held(b arcproto.Button) bool     // 押されている
func (i Input) Pressed(b arcproto.Button) bool  // このフレームで押された
func (i Input) Released(b arcproto.Button) bool // このフレームで離された
```

### `Frame` の描画 API

```go
// Frame は 1 フレーム分の描画コマンドを蓄積する。
type Frame struct {
	batch arcproto.Batch
	page  uint8
}

func (f *Frame) Page(p uint8) *Frame                 // 以降の対象ページ
func (f *Frame) Clear(idx uint8)
func (f *Frame) DrawSprite(s *Sprite, x, y int, opts ...DrawOption)
func (f *Frame) FillRect(x, y, w, h int, idx uint8, opts ...DrawOption)
func (f *Frame) CopyRect(src, dst Rect, srcPage, dstPage uint8)
func (f *Frame) SetViewport(x, y int)
func (f *Frame) SetDisplayPage(p uint8)
```

### 描画オプション

```go
type DrawOption func(*drawParams)

func Pivot(x, y int) DrawOption
func Rotate(r arcproto.Rotation) DrawOption
func Scale(v float64) DrawOption
func ScaleXY(sx, sy float64) DrawOption
func Blend(m arcproto.BlendMode) DrawOption
func OnPage(p uint8) DrawOption
```

回転量は `arcproto.Degrees(45)` / `arcproto.Turns(0.125)` で生成する（024 の R4）。

### 変換なし描画の分岐（R5）

```go
func (f *Frame) DrawSprite(s *Sprite, x, y int, opts ...DrawOption) {
	p := defaultDrawParams()
	for _, o := range opts {
		o(&p)
	}

	if p.isIdentity() {
		// 回転なし・等倍・ピボット未指定 → 安価な blit_rect
		f.batch.Add(arcproto.CmdBlitRect, arcproto.BlitRect{
			Page: p.page, X: uint16(x), Y: uint16(y),
			W: uint16(s.W()), H: uint16(s.H()),
			Blend: p.blend, Pixels: s.Pixels(),
		}.Encode())
		return
	}

	f.batch.Add(arcproto.CmdBlitRectTransform, arcproto.BlitRectTransform{
		// ... 変換パラメータ付き
	}.Encode())
}
```

### 実行ループ（R2 / R13）

```go
func Run(ctx context.Context, d *Device, app App) error {
	if err := app.Init(d); err != nil {
		return err
	}
	if c, ok := app.(Closer); ok {
		defer c.Close()
	}

	f := d.newFrame()   // R3: フレーム間で再利用
	start := time.Now()
	var prev time.Duration

	for {
		tick, ok, err := d.nextTick(ctx)   // vsync + 入力を待つ。滞留は破棄（R13）
		if err != nil {
			return err
		}
		if !ok {
			return nil   // shutdown
		}

		tick.Elapsed = time.Since(start)
		tick.Delta = tick.Elapsed - prev
		prev = tick.Elapsed

		f.reset()
		app.Update(f, tick)

		if err := d.flush(f); err != nil {   // R3: batch を 1 メッセージで送信
			return err
		}
	}
}
```

`nextTick` は vsync イベントのチャネルから**最後の 1 件まで読み捨てて最新を採用**し、
破棄件数を `Tick.Skipped` に入れる。入力スナップショットは
破棄した分の pressed / released を **OR で合成**して失わないようにする
（026 のラッチ方式と整合させる）。

### デモの載せ替え（R12）

移設先: `features/neurom/internal/demo/`

```
features/neurom/internal/demo/
  demo.go       // App 実装。シーンのローテート管理
  scenes.go     // 7 シーンの Init / Update
  sprites.go    // diamondSprite
```

`cpu.go` は以下程度に縮小する。

```go
func (c *CPUModule) Start(ctx context.Context, b bus.Bus) error {
	dev, err := arc.Embed(b)
	if err != nil {
		return err
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer dev.Close()
		if err := arc.Run(ctx, dev, demo.New()); err != nil {
			log.Printf("[CPU] demo exited: %v", err)
		}
	}()
	return nil
}
```

シーンの書き換え例（シーン 3）:

```go
func (s *sceneTransform) Init(d *arc.Device) error {
	return d.SetPalette(0, []arc.Color{
		{R: 20, G: 20, B: 40, A: 255},
		{R: 255, G: 100, B: 50, A: 255},
	})
}

func (s *sceneTransform) Update(f *arc.Frame, t arc.Tick) {
	// R8: フレーム数ではなく経過時間で回す（リフレッシュレート非依存）
	rot := arcproto.Turns(t.Elapsed.Seconds() * rotationsPerSecond)

	f.Clear(0)
	f.DrawSprite(diamond, 128, 106, arc.Pivot(4, 4), arc.Rotate(rot))
	f.DrawSprite(diamond, 64, 106, arc.Pivot(4, 7), arc.Rotate(rot))
	f.DrawSprite(diamond, 192, 106, arc.Pivot(4, 4), arc.Rotate(rot), arc.Scale(2.0))
}
```

`0x0100`、`uint8((frame*2)%256)`、`b bus.Bus`、`c *CPUModule` がすべて消える。

### アニメーション速度の換算（R14）

現状の更新レートは 125Hz（8ms）、vsync は既定 60Hz。
フレーム数ベースの係数をそのまま使うと**約 2 倍遅くなる**ため、時間ベースへ換算する。

| シーン | 現状の式 | 換算方針 |
|---|---|---|
| 1 Palette | `frame % 256` | 経過時間から 1 周の秒数を定めて算出 |
| 2 BlitRect | `frame % 2` | 点滅周期を秒で定義 |
| 3 Transform | `(frame*2) % 256` | 毎秒の回転数を定義（約 0.98 回転/秒 相当） |
| 4 AlphaBlend | `sin(frame*0.08)` | 周期を秒で定義（約 1.25 秒/周期 相当） |
| 5 Scroll | 1 px/tick | 毎秒のスクロール px を定義（125 px/秒 相当） |
| 6 PageComposite | `sin(frame*0.05)`, `cos(frame*0.07)` | 同上 |
| 7 LargeMapScroll | `sin(frame*0.02)`, `sin(frame*0.03)` | 同上 |

シーン 5・6 のスクロールは `copy_rect` による 1px シフトの累積であるため、
時間ベースにすると 1 フレームあたりのシフト量が非整数になる。
**累積誤差を保持して整数 px 単位で消化する**方式にする。

### 変更対象ファイル

| ファイル | 変更内容 |
|---|---|
| `features/neurom/arc/` | 新規。SDK 一式 |
| `features/neurom/internal/demo/` | 新規。デモ本体を移設し SDK ベースへ書き換え |
| `features/neurom/internal/modules/cpu/cpu.go` | デモとエンコーダを削除し、SDK ホストへ縮小（548 行 → 数十行） |
| `features/neurom/internal/modules/cpu/cpu_test.go` | ホストとしての最小テストへ改訂 |
| `features/neurom/integration/` | SDK 経由の統合テスト追加 |

`cpu_test.go` は現状 `dummyBus` で最初の 1 メッセージの `Source` が `"CPU"` かを
確認するだけ（40 行）なので、移設の障害にはならない。

### 本仕様で扱わないこと（スコープ外）

- TCP 接続（`arc.Dial`）→ 029
- 別バイナリ化（`features/metov/`）→ 029
- 外部トランスポートのエンベロープ変更 → 028
- `cpu` モジュールの廃止判断 → 029
- 多言語バインディング

## 検証シナリオ (Verification Scenarios)

1. `features/neurom/arc/` を作成し、`Device` / `Frame` / `Sprite` / `Input` / `Tick` / `App` / `Run` を実装する
2. `arc` が `internal/modules/*` に依存していないことを確認する
3. `arc.Embed(bus)` で in-process 接続が確立できることを単体テストで確認する
4. `Frame` に描画コマンドを 864 件積み、送信されるバスメッセージが 1 件（バッチ）であることを確認する
5. コマンド 0 件のフレームで何も送信されないことを確認する
6. `Frame` がフレーム間で再利用され、毎フレームの確保が発生しないことをベンチマークで確認する
7. 変換オプション無しの `DrawSprite` が `blit_rect` を、変換オプション有りが `blit_rect_transform` を発行することを確認する
8. `Rotate` / `Scale` / `Pivot` / `Blend` の各オプションが期待どおりのバイト列になることを確認する
9. `Scale(1.0)` が `0x0100`、`Scale(2.0)` が `0x0200` になることを確認する
10. `Sprite` の生成時に `len(pixels) != w*h` がエラーになることを確認する
11. 026 の入力スナップショットが `Tick.Input` の `Held` / `Pressed` / `Released` に正しく反映されることを確認する
12. `Update` を意図的に遅延させ、滞留した vsync が破棄されて `Tick.Skipped` に計上されること、および破棄分の pressed / released が失われないことを確認する
13. `Device` の同期読み出し（矩形・パレット）が値を返し、応答が無い場合にタイムアウトエラーになることを確認する
14. デモ 7 シーンを `features/neurom/internal/demo/` へ移設し、`arc` SDK ベースへ書き換える
15. デモコード内に `bus.Bus` / `arcproto` / `binary.BigEndian` が現れないことを確認する
16. アニメーション係数を時間ベースへ換算し、`--refresh-rate 60` と `--refresh-rate 30` で見かけの速度が同等であることを目視確認する
17. `cpu.go` をデモ起動のみの薄いホストへ縮小する
18. `scripts/process/build.sh` を実行し、全ビルドと単体テストが PASS することを確認する
19. `./bin/neurom.exe` を起動し、7 シーンが意図どおり表示されることを目視確認する
20. **シーン 2 が画面全体を正しく敷き詰めるようになったこと**（従来は破棄により部分描画だった）を目視確認する
21. `./bin/neurom.exe --headless --stats-port 8080` で `/stats` を取得し、破棄件数が 0 であることを確認する

## テスト項目 (Testing for the Requirements)

| 要件 | 検証方法 | コマンド / 手段 |
|---|---|---|
| R1: パッケージ配置と依存 | `arc` が `internal/modules` に依存しない | `cd features/neurom && go list -deps ./arc \| grep "internal/modules"`（0 件であること） |
| R2: 実行ループ | `App` の `Init` が 1 回、`Update` が vsync ごとに呼ばれる | `cd features/neurom && go test -v -count=1 -run "TestRunLifecycle" ./arc/...` |
| R2: Closer | `Closer` 実装時に `Close` が呼ばれる | `cd features/neurom && go test -v -count=1 -run "TestRunCloser" ./arc/...` |
| R3: バッチ化 | 864 コマンドがバスメッセージ 1 件になる | `cd features/neurom && go test -v -count=1 -run "TestFrameBatching" ./arc/...` |
| R3: 空フレーム | コマンド 0 件で送信が発生しない | `cd features/neurom && go test -v -count=1 -run "TestFrameEmpty" ./arc/...` |
| R3: 再利用 | フレームあたりの確保が 0 に近い | `cd features/neurom && go test -bench "BenchmarkFrameReuse" -benchmem -run "^$" ./arc/...` |
| R4: 単位変換 | 度数・回転数・拡大率の変換が正しい | `cd features/neurom && go test -v -count=1 -run "TestDrawOption" ./arc/...` |
| R5: 変換なし最適化 | オプション無しで `blit_rect` が選ばれる | `cd features/neurom && go test -v -count=1 -run "TestDrawSpriteFastPath" ./arc/...` |
| R6: Sprite 検証 | 不整合サイズでエラー | `cd features/neurom && go test -v -count=1 -run "TestNewSprite" ./arc/...` |
| R7: 入力 API | スナップショットが Held / Pressed / Released に反映 | `cd features/neurom && go test -v -count=1 -run "TestInputAPI" ./integration/...` |
| R8: 時間 API | `Elapsed` / `Delta` が単調かつ妥当 | `cd features/neurom && go test -v -count=1 -run "TestTickTiming" ./arc/...` |
| R9: Embed | in-process 接続で描画がバスに届く | `cd features/neurom && go test -v -count=1 -run "TestEmbed" ./integration/...` |
| R10: 初期化系 API | パレット・ページ・ビューポート設定が反映される | `cd features/neurom && go test -v -count=1 -run "TestDeviceSetup" ./integration/...` |
| R11: 同期読み出し | 値が返る / タイムアウトでエラー | `cd features/neurom && go test -v -count=1 -run "TestDeviceRead\|TestDeviceReadTimeout" ./integration/...` |
| R12: デモ載せ替え | デモコードに低水準要素が現れない | `grep -rn "bus\.Bus\|arcproto\|binary.BigEndian" features/neurom/internal/demo/`（0 件） |
| R12: cpu の縮小 | `cpu.go` の行数が大幅に減少 | `wc -l features/neurom/internal/modules/cpu/cpu.go`（100 行未満を目標） |
| R13: フレームスキップ | 遅延時に滞留 vsync が破棄され `Skipped` に計上 | `cd features/neurom && go test -v -count=1 -run "TestFrameSkip" ./arc/...` |
| R13: 入力の非欠落 | 破棄した vsync の pressed が合成される | `cd features/neurom && go test -v -count=1 -run "TestFrameSkipInputMerge" ./arc/...` |
| R14: シーン 2 の改善 | 画面全体が敷き詰められる | `./bin/neurom.exe` でシーン 2 を目視 |
| R14: 破棄 0 件 | `/stats` の破棄件数が 0 | `./bin/neurom.exe --headless --stats-port 8080` + `cd features/stats && go run . --endpoint http://127.0.0.1:8080/stats` |
| R14: レート非依存 | 60Hz と 30Hz で見かけの速度が同等 | `./bin/neurom.exe --refresh-rate 60` と `--refresh-rate 30` を目視比較 |
| R15: パレット生成 | HSV ランプ生成の境界値が正しい | `cd features/neurom && go test -v -count=1 -run "TestHSV" ./arc/...` |
| R16: 図形補助 | 単色矩形が期待バイト列になる | `cd features/neurom && go test -v -count=1 -run "TestFillRect" ./arc/...` |
| R17: サンプル | Example テストが PASS | `cd features/neurom && go test -v -count=1 -run "Example" ./arc/...` |
| 全体リグレッション | ビルド + 全単体テスト | `scripts/process/build.sh` |
| VRAM リグレッション | VRAM 系統合テスト全件 PASS | `scripts/process/integration_test.sh --specify "TestPageManagementIntegration\|TestPageDrawIsolationIntegration\|TestVRAMEnhancement\|TestPaletteUpdate\|TestVRAMMonitorIntegration"` |

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
