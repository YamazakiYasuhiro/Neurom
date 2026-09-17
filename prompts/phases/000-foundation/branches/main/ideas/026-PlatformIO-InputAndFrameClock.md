# 026: 入力デバイスとフレームクロックの実装

## 背景 (Background)

### 問題 1: 入力経路が一切存在しない

Neurom は「レトロゲーム機の仮想ハードウェア」を名乗るが、**入力を読む手段が皆無**である。

- `features/neurom/internal/modules/io/io.go` は **48 行の空スケルトン**。
  `system` トピックを購読して shutdown を待つだけで、自分のトピックの購読すら行っていない。

  ```go
  func (i *IOModule) Start(ctx context.Context, b bus.Bus) error {
  	sysCh, err := b.Subscribe("system")
  	if err != nil {
  		return err
  	}
  	// io トピックの購読が無い
  ```

- `features/neurom/internal/modules/monitor/monitor.go` を
  `key` / `Key` / `Input` / `Mouse` / `touch` で検索すると **ヒット 0 件**。
  `RunMain` の `app.Main` イベントループ（150-200 行）が処理しているのは
  `lifecycle.Event` / `size.Event` / `paint.Event` のみで、
  `golang.org/x/mobile/event/key` は import もされていない。

デモが 3 秒ごとの自動シーン切替（`cpu.go` の `SceneDuration`）になっているのは、
この制約の帰結と考えられる。**入力が無いため、インタラクティブな拡張ソフトウェアが原理的に書けない。**

### 問題 2: フレームクロックが公開されていない

Monitor が発行するイベントは `stats_data`（`monitor.go` 110-116 行）と
`system` / `Shutdown`（202-217 行）の 2 種類のみで、
**描画タイミングを通知するイベントが存在しない**。

そのため `cpu.go` は独自の 8ms ticker で回っている（`cpu.go` 16 行, 84 行）。

```go
TickInterval = 8 * time.Millisecond
// ...
ticker := time.NewTicker(TickInterval)
```

これは実際の描画レートと同期していない。拡張ソフトウェアが自分の更新を
描画に合わせる手段が無く、各拡張ソフトが独自にタイマーを持つことになる。

### 問題 3: 「dirty 駆動の再描画」ではクロックにならない

素朴に「`onPaint` の後に vsync を発行する」と実装すると **デッドロックする**。

現在の再描画は dirty 駆動である（`monitor.go` 303-309 行）。

```go
func (m *MonitorModule) markDirty() {
	wasDirty := m.dirty
	m.dirty = true
	if !wasDirty && m.appObj != nil && !m.config.Headless {
		m.appObj.Send(paint.Event{})
	}
}
```

つまり **描画が起きるのは誰かが VRAM を書き換えたときだけ**。
拡張ソフトウェアが「vsync を待ってから描く」設計だと、

1. 拡張ソフトが vsync を待つ
2. 誰も描かないので dirty にならない
3. `paint.Event` が飛ばない
4. vsync が発行されない → 1 に戻る（永久に停止）

したがって **フレームクロックは描画完了ではなく、独立した時間駆動で発行しなければならない**。

### 問題 4: `RefreshRate` が使われていない

`MonitorConfig` には `RefreshRate int` フィールドが定義されている（`monitor.go` 37-41 行）が、
`cmd/main.go` はこれを設定しておらず（`main.go` 61-64 行では `Headless` と `OnClose` のみ）、
Monitor 内部でも参照されていない。**宣言されただけの死んだフィールド**である。
フレームクロックの実装にあたって、このフィールドを本来の用途で活かす。

### 本仕様の位置づけ

拡張ソフトウェア分離（024〜029）の第 3 ステップ。
**「ゲームを書くために必要な最低限のハードウェア能力」を基盤側に用意する。**
描画（VRAM）は既に十分だが、入力と時間の 2 つが欠けている。

- 前提: [024-ARCProtocol-PackageExtraction](file://prompts/phases/000-foundation/branches/main/ideas/024-ARCProtocol-PackageExtraction.md)（コマンド定義の置き場所）
- 併走可: [025-BusProtocol-BatchAndAddressing](file://prompts/phases/000-foundation/branches/main/ideas/025-BusProtocol-BatchAndAddressing.md)（相関 ID を `get_input_state` に利用）
- 後続: 027（SDK が入力と vsync を `Tick` として提供）

## 要件 (Requirements)

### 必須要件

1. **R1: Monitor による生キー入力の捕捉**
   - `RunMain` の `app.Main` イベントループに `golang.org/x/mobile/event/key` の
     `key.Event` 処理を追加する。
   - 捕捉したイベントを生の形でバスへ発行する（物理キーコード・押下方向・修飾キー）。
   - Monitor は**論理ボタンへの変換を行わない**（Monitor はあくまで物理デバイス層）。
   - 既存の `lifecycle` / `size` / `paint` の処理と shutdown 経路に影響を与えないこと。

2. **R2: フレームクロック（vsync）の発行**
   - Monitor が**時間駆動の独立したクロック**で vsync イベントを発行する。
   - **描画完了（`paint.Event` / `onPaint`）を発行契機にしてはならない**（問題 3 のデッドロック回避）。
   - イベント payload にフレーム連番と発行時刻を含める。
   - ウィンドウモードとヘッドレスモードの**両方**で発行されること。
   - レート決定の優先順位を定める: `MonitorConfig.RefreshRate` → 既定値 60Hz。

3. **R3: リフレッシュレートの外部指定**
   - `cmd/main.go` に `--refresh-rate` フラグを追加し、`MonitorConfig.RefreshRate` へ渡す。
   - 未指定時の既定値は 60。
   - 不正値（0 以下、極端に大きい値）は既定値に丸める。

4. **R4: IO モジュールの入力デバイス化**
   - IO モジュールが自分のコマンドトピックとイベントトピックを購読・発行するようにする
     （現状は自分のトピックを購読していない）。
   - Monitor が発行する生キーイベントを購読し、**論理ボタンの状態機械**を保持する。
   - 保持する状態:
     - 現在押されているボタン（held）
     - 前回スナップショット以降に押し下げられたボタン（pressed ラッチ）
     - 前回スナップショット以降に離されたボタン（released ラッチ）
   - **ラッチ方式が必須**である。単に現在状態だけを配信すると、
     1 フレーム内に押下と離上が起きた場合に入力が消滅するため。

5. **R5: フレーム境界での入力スナップショット配信**
   - IO は vsync を購読し、**vsync ごとに 1 回だけ**入力スナップショットイベントを発行する。
   - 発行後に pressed / released ラッチをクリアする（エッジのちょうど 1 回配信を保証）。
   - payload にフレーム連番を含め、どの vsync に対応するスナップショットかを識別できること。
   - スナップショットは**状態に変化がなくても毎フレーム発行する**
     （拡張ソフトウェアが「入力が届かない」と「入力が無い」を区別できるようにする）。

6. **R6: 論理ボタンの定義と既定キーマッピング**
   - レトロゲーム機相当の論理ボタンを定義する。
     方向 4 種（上下左右）、アクション 4 種、Start、Select。
   - 物理キーから論理ボタンへの既定マッピングを定義する
     （方向は矢印キー、アクションは Z / X / A / S、Start は Enter、Select は 右Shift 等）。
   - マッピングは IO モジュール内のテーブルとして定義し、将来の設定可能化に備えて分離しておく。

7. **R7: オンデマンド入力取得**
   - IO に入力状態を問い合わせるコマンドを追加し、応答イベントを返す。
   - 025 の相関 ID を用いて要求と応答を対応付ける。
   - この経路では pressed / released ラッチを**クリアしない**
     （R5 のフレーム同期配信と競合させないため。あくまで診断・デバッグ用途）。

8. **R8: ヘッドレスモードでの動作**
   - ヘッドレスモードでは物理キー入力が存在しないため、held は常に空となる。
   - それでも vsync と入力スナップショットは規定レートで発行され続けること
     （ヘッドレスでの自動テストが成立するために必須）。

9. **R9: 既存の停止処理への非干渉**
   - `020-AppMainShutdownHang` および `005-GracefulShutdown` で確立した停止シーケンスを壊さないこと。
   - 追加したクロック用 goroutine が `ctx.Done()` および shutdown コマンドで確実に終了し、
     `Stop()` の `wg.Wait()` がハングしないこと。

### 任意要件

10. **R10: 入力イベントの記録・再生**
    - 入力スナップショットの列を記録し、後から再生する仕組み。
    - 決定論的なリグレッションテストや TAS 的な用途に有用。
    - 本仕様では**プロトコル上の余地を残すのみ**とし、実装は将来に委ねてよい。

11. **R11: 実描画 FPS の vsync への同梱**
    - vsync payload に、直近の実描画 FPS を診断情報として含める。
    - クロックレートと実描画レートの乖離を拡張ソフトウェアが検知できるようにする。

12. **R12: 修飾キーと文字入力の透過**
    - 論理ボタンにマップされない生キー（文字入力等）も、生イベントとして
      購読可能な状態を維持する。デバッガやツール類が利用できるようにする。

## 実現方針 (Implementation Approach)

### 全体のデータフロー

```
      ┌──────────┐  key.Event
      │  OS/窓   │───────────────┐
      └──────────┘               ▼
                          ┌─────────────┐
                          │   Monitor   │
                          │             │
                          │ ・生キー発行 │──── evt.monitor / key_raw ────┐
                          │ ・クロック   │──── evt.monitor / vsync ──────┤
                          └─────────────┘                               │
                                                                        ▼
                                                                 ┌─────────────┐
                                                                 │     IO      │
                                                                 │ 論理ボタン   │
                                                                 │ 状態機械     │
                                                                 └─────────────┘
                                                                        │
                                        evt.io / input_state            │
      ┌────────────────────┐◄──────────────────────────────────────────┘
      │ 拡張ソフトウェア     │
      │ (027 の SDK 経由)   │──── cmd.vram / batch ────► VRAM
      └────────────────────┘
```

Monitor は物理層（窓とクロックを所有）、IO は論理層（ボタン意味づけと状態保持）という
責務分離にする。この分割により、将来ゲームパッド等の別の物理入力源を
Monitor 以外のモジュールとして追加しても、IO 側は変更不要になる。

### コマンド / イベントの追加

`arcproto` に以下を追加する（024 で作ったパッケージに集約）。

| 種別 | Target | payload レイアウト | 発行元 |
|---|---|---|---|
| イベント | `key_raw` | `[code:u16][direction:u8][modifiers:u8][rune:u32]` | Monitor |
| イベント | `vsync` | `[frame:u64][unix_nano:u64][fps_milli:u32]` | Monitor |
| イベント | `input_state` | `[frame:u64][held:u16][pressed:u16][released:u16]` | IO |
| コマンド | `get_input_state` | なし（相関 ID で応答を識別） | 拡張ソフト → IO |

論理ボタンは 16 種以内なので `u16` ビットマスクで表現する。

```go
package arcproto

type Button uint16

const (
	PadUp Button = 1 << iota
	PadDown
	PadLeft
	PadRight
	PadA
	PadB
	PadX
	PadY
	PadStart
	PadSelect
)
```

### フレームクロックの実装

`paint.Event` から独立させることが本質。Monitor の `Start` で goroutine を 1 本起こす。

```go
func (m *MonitorModule) runFrameClock(ctx context.Context) {
	rate := m.config.RefreshRate
	if rate <= 0 || rate > 1000 {
		rate = DefaultRefreshRate   // 60
	}
	ticker := time.NewTicker(time.Second / time.Duration(rate))
	defer ticker.Stop()

	var frame uint64
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.clockStop:      // shutdown コマンド経路
			return
		case <-ticker.C:
			m.publishVsync(frame)
			frame++
		}
	}
}
```

ウィンドウモードでもヘッドレスモードでも同一の goroutine を使うため、
R8（ヘッドレスでの継続発行）が自動的に満たされる。

なお `hrtimer_windows.go` / `hrtimer_other.go` に高分解能タイマーの
プラットフォーム別実装がすでに存在する（`internal/modules/vram/`）。
`time.Ticker` の分解能が Windows で不足する場合はこれを参考にできる。

### 生キー捕捉の実装

`RunMain` のイベントループに 1 ケース追加するだけで済む。

```go
import "golang.org/x/mobile/event/key"

// ...
for e := range a.Events() {
	switch e := a.Filter(e).(type) {
	case lifecycle.Event:
		// 既存処理（変更しない）
	case size.Event:
		// 既存処理（変更しない）
	case paint.Event:
		// 既存処理（変更しない）
	case key.Event:
		m.publishKeyRaw(e)      // 追加
	}
}
```

`key.Event` は `Code`（物理キー）、`Direction`（`DirPress` / `DirRelease` / `DirNone`）、
`Modifiers`、`Rune` を持つ。`DirNone` はリピートを含むため、
IO 側の状態機械では `DirPress` / `DirRelease` のみを状態遷移に使う。

### IO モジュールの状態機械

```go
type IOModule struct {
	wg sync.WaitGroup
	mu sync.Mutex

	held     arcproto.Button
	pressed  arcproto.Button   // ラッチ
	released arcproto.Button   // ラッチ
}

func (i *IOModule) onKeyRaw(ev arcproto.KeyRaw) {
	btn, ok := defaultKeyMap[ev.Code]
	if !ok {
		return   // 論理ボタン未割当のキーは無視（生イベントは他が購読可能）
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	switch ev.Direction {
	case arcproto.DirPress:
		if i.held&btn == 0 {
			i.pressed |= btn      // 立ち上がりのみラッチ
		}
		i.held |= btn
	case arcproto.DirRelease:
		if i.held&btn != 0 {
			i.released |= btn
		}
		i.held &^= btn
	}
}

func (i *IOModule) onVsync(frame uint64) {
	i.mu.Lock()
	snap := arcproto.InputState{
		Frame: frame, Held: i.held,
		Pressed: i.pressed, Released: i.released,
	}
	i.pressed = 0     // R5: 配信したのでラッチをクリア
	i.released = 0
	i.mu.Unlock()

	i.bus.Publish(arcproto.TopicEvtIO, ...)
}
```

ラッチ方式により、1 フレーム内に押下と離上が起きても
`pressed` と `released` の両方が立った状態で 1 回だけ配信され、入力が消滅しない。

### 既定キーマッピング

| 論理ボタン | 既定の物理キー |
|---|---|
| PadUp / PadDown / PadLeft / PadRight | 矢印キー 上 / 下 / 左 / 右 |
| PadA | Z |
| PadB | X |
| PadX | A |
| PadY | S |
| PadStart | Enter |
| PadSelect | 右Shift |

テーブルは `internal/modules/io/keymap.go` に分離し、
将来の設定ファイル対応・キーコンフィグ機能の追加点を明確にしておく。

### 停止処理への配慮（R9）

`020-AppMainShutdownHang` / `005-GracefulShutdown` で確立した停止シーケンスに
新規 goroutine を 2 本（Monitor のクロック、IO の入力処理）追加することになる。

- いずれも `ctx.Done()` と shutdown コマンドの両方で終了すること
- `wg.Add` / `wg.Done` の対応を守り、`Stop()` の `wg.Wait()` がハングしないこと
- Monitor のクロック goroutine は `RunMain`（`app.Main`）とは独立に動くため、
  ウィンドウが閉じた後も `ctx` がキャンセルされるまで回り続ける点に注意する
  （`publishVsync` の宛先が閉じた `ChannelBus` でも安全であることを確認する。
  `ChannelBus.Publish` は `closed` フラグで早期 return するため問題ないが、テストで確認する）

### 変更対象ファイル

| ファイル | 変更内容 |
|---|---|
| `features/neurom/arcproto/` | `Button`、`KeyRaw`、`Vsync`、`InputState`、`get_input_state` の定義 |
| `features/neurom/internal/modules/monitor/monitor.go` | `key.Event` 捕捉、フレームクロック goroutine、`--refresh-rate` の反映 |
| `features/neurom/internal/modules/io/io.go` | 自トピック購読、状態機械、スナップショット発行 |
| `features/neurom/internal/modules/io/keymap.go` | 新規。既定キーマッピングテーブル |
| `features/neurom/cmd/main.go` | `--refresh-rate` フラグ追加、`MonitorConfig.RefreshRate` の設定 |
| `features/neurom/integration/` | 入力・vsync の統合テスト追加 |

### 本仕様で扱わないこと（スコープ外）

- SDK 側の `Tick` / `Input` 型の提供 → 027
- ゲームパッド・マウス・タッチ入力（論理ボタンの抽象は用意するが物理源は追加しない）
- キーコンフィグの設定ファイル対応（テーブルを分離するのみ）
- 入力の記録・再生の実装（R10 でプロトコル余地のみ確保）
- 音声（APU）

## 検証シナリオ (Verification Scenarios)

1. `arcproto` に `Button` ビットマスク、`KeyRaw` / `Vsync` / `InputState` の型と Encode / Decode、`get_input_state` コマンドを追加する
2. `cmd/main.go` に `--refresh-rate` フラグを追加し、`MonitorConfig.RefreshRate` に渡す
3. Monitor にフレームクロック goroutine を実装し、`paint.Event` とは独立に vsync を発行する
4. `./bin/neurom.exe --headless --refresh-rate 60` を起動し、vsync が約 60 件/秒 発行されることを確認する
5. `./bin/neurom.exe --headless --refresh-rate 30` を起動し、発行レートが約 30 件/秒 に変わることを確認する
6. **何も描画しない状態でも vsync が発行され続けること**を確認する（問題 3 のデッドロックが起きないことの確認）
7. Monitor の `app.Main` ループに `key.Event` 処理を追加し、生キーイベントを発行する
8. IO モジュールに自トピック購読・論理ボタン状態機械・キーマップテーブルを実装する
9. IO が vsync ごとに入力スナップショットを 1 回だけ発行することを確認する
10. 入力に変化が無いフレームでもスナップショットが発行されることを確認する
11. 生キーイベントを注入し、押下 → 継続 → 離上 の遷移で held / pressed / released が期待どおり変化することを確認する
12. **1 フレーム内に押下と離上を注入し、pressed と released の両方が立った状態で配信されること**（入力が消滅しないこと）を確認する
13. pressed / released が次フレームのスナップショットではクリアされていることを確認する
14. `get_input_state` コマンドを相関 ID 付きで発行し、応答が同じ ID で返り、かつラッチがクリアされないことを確認する
15. ウィンドウモードで `./bin/neurom.exe` を起動し、矢印キー・Z・X 等を実際に押して held が変化することを手動確認する
16. ウィンドウを閉じて停止させ、追加した goroutine がハングせず `Stop()` が完了することを確認する
17. shutdown コマンド経由（`--headless` + SIGINT）でも同様に停止することを確認する
18. `scripts/process/build.sh` を実行し、全ビルドと単体テストが PASS することを確認する

## テスト項目 (Testing for the Requirements)

| 要件 | 検証方法 | コマンド / 手段 |
|---|---|---|
| R1: 生キー捕捉 | `key.Event` から `key_raw` イベントが生成される | `cd features/neurom && go test -v -count=1 -run "TestKeyRawPublish" ./internal/modules/monitor/...` |
| R2: vsync 発行 | ヘッドレスで規定レートの vsync が発行される | `cd features/neurom && go test -v -count=1 -run "TestFrameClock" ./integration/...` |
| R2: 描画非依存 | 一切描画しない状態でも vsync が継続発行される | `cd features/neurom && go test -v -count=1 -run "TestFrameClockWithoutDraw" ./integration/...` |
| R3: レート指定 | `--refresh-rate 30` で発行レートが約半分になる | `cd features/neurom && go test -v -count=1 -run "TestRefreshRateFlag" ./integration/...` |
| R3: 不正値の丸め | 0 / 負値 / 過大値が既定値 60 に丸められる | `cd features/neurom && go test -v -count=1 -run "TestRefreshRateClamp" ./internal/modules/monitor/...` |
| R4: 状態機械 | 押下 / 継続 / 離上で held・pressed・released が期待どおり遷移 | `cd features/neurom && go test -v -count=1 -run "TestInputStateMachine" ./internal/modules/io/...` |
| R4: ラッチの必要性 | 1 フレーム内の押下+離上で入力が消滅しない | `cd features/neurom && go test -v -count=1 -run "TestInputLatchWithinFrame" ./internal/modules/io/...` |
| R5: フレーム同期配信 | vsync 1 件に対しスナップショットが正確に 1 件 | `cd features/neurom && go test -v -count=1 -run "TestInputSnapshotPerVsync" ./integration/...` |
| R5: ラッチのクリア | 次フレームで pressed / released が 0 になる | 同上テスト内で検証 |
| R5: 無変化でも配信 | 入力変化が無いフレームでもスナップショットが届く | `cd features/neurom && go test -v -count=1 -run "TestInputSnapshotAlways" ./integration/...` |
| R6: キーマッピング | 全論理ボタンについて既定キーからの変換が正しい | `cd features/neurom && go test -v -count=1 -run "TestDefaultKeyMap" ./internal/modules/io/...` |
| R7: オンデマンド取得 | `get_input_state` の応答に要求 ID が複写され、ラッチが保持される | `cd features/neurom && go test -v -count=1 -run "TestGetInputState" ./integration/...` |
| R8: ヘッドレス動作 | ヘッドレスで vsync とスナップショットが継続発行される | `./bin/neurom.exe --headless --refresh-rate 60` を 5 秒間起動し件数をログで確認 |
| R9: 停止の非干渉 | 既存の停止テストが全件 PASS | `scripts/process/integration_test.sh --specify "TestGracefulShutdown\|TestShutdown"` |
| R9: goroutine リーク | 停止後に追加 goroutine が残らない | `cd features/neurom && go test -v -count=1 -run "TestNoGoroutineLeak" ./integration/...` |
| R11: 実 FPS 同梱 | vsync payload の FPS が実測値と整合する | `cd features/neurom && go test -v -count=1 -run "TestVsyncFPSField" ./integration/...` |
| R12: 未割当キーの透過 | 論理ボタン未割当キーでも `key_raw` は発行される | `cd features/neurom && go test -v -count=1 -run "TestUnmappedKeyRaw" ./internal/modules/io/...` |
| 手動確認: 実キー入力 | 実際のキーボード操作で held が変化する | `./bin/neurom.exe` を起動し矢印キー・Z・X を押して統計/ログで確認 |
| 全体リグレッション | ビルド + 全単体テスト | `scripts/process/build.sh` |

### 補足: 検証スクリプトの現状

`scripts/process/build.sh` は各 feature で `go list ./... | grep -v '/tests/'` を実行するため、
`features/neurom/integration/` も単体テストとして実行される。実質的な全体検証ゲートは `build.sh` である。
`scripts/process/integration_test.sh` はリポジトリルートの `tests/go.mod` を前提とするが、
現状 `tests/` は存在しないため warn を出して exit 0 する（no-op）。
上表の `integration_test.sh --specify` は統合テストが `tests/` へ移設された後に有効となり、
それまでは `build.sh` および `cd features/neurom && go test -run ... ./integration/...` で代替する。


> [!NOTE]
> `--categories` および階層分離は仕様 030 実装後に使用可能。それ以前は `integration_test.sh` が無効で、統合テストは `build.sh` 側で実行されていた。
## 対応ステータス

- **ステータス**: 未着手
- **実装計画**: 未作成
