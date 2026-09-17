# 030: テスト基盤の整備 — ランナーの実効化と検証階層の再定義

## 背景

仕様 024 の実装中に、テスト基盤が**実態と乖離している**ことが判明した。
以下はすべて実測または実装の読解によって確認した事実である。

### 1. `integration_test.sh` は無条件に何も実行しない

`scripts/process/integration_test.sh` はリポジトリルートの `tests/` ディレクトリと
`tests/go.mod` の存在を前提としている（106-116 行）。
しかし**リポジトリ内に `tests/` ディレクトリは一つも存在しない**（`find` で確認）。

結果としてこのスクリプトは常に以下を出力して**終了コード 0 で成功する**。

```text
[WARN] tests/ directory not found — no integration tests to run.
```

統合テストが緑であることが何も保証していない。これが最も重大な問題である。

### 2. 統合テストは `build.sh` が単体テストとして実行している

実際の統合テストは `features/neurom/integration/` に存在する。
`build.sh` は `go list ./... | grep -v '/tests/' | grep -v '/tests$'` で
統合テストを除外しようとしているが（115 行）、
このパスには `/tests/` が含まれないため**除外されずに実行される**。

つまり階層の意図と実態が逆転している。

- 意図: `build.sh` = 単体テスト、`integration_test.sh` = 統合テスト
- 実態: `build.sh` = 単体テスト + 統合テスト、`integration_test.sh` = 何もしない

実測: `build.sh` 全体が約 50 秒、うち `integration` パッケージ単体で約 41 秒。
**所要時間の 8 割が統合テストである。**
開発中の高速なフィードバックループが存在しない。

### 3. スキルが指示するオプションが実装されていない

`create-specification` スキルは仕様書に以下の形式で検証コマンドを書くことを義務付けている。

```text
scripts/process/integration_test.sh --categories "common"
```

しかし `integration_test.sh` は `--specify` のみを受け付け、
`--categories` を渡すと `Unknown option` で**終了コード 1 で失敗する**。

さらにスキルが列挙するカテゴリ（`common`, `llm`, `taskengine`, `template`, `gui`）は
**別プロジェクト由来の値**であり、Neurom には対応する概念が存在しない。

このスキルに従って書かれた仕様書（024 から 029 を含む）の検証コマンドは
**実行不可能である**。

### 4. `build.sh` は失敗時に総合判定を出力しない

`build.sh` は `set -euo pipefail` の下で `main` から `build_go` を呼ぶ（161 行）。
単体テストが失敗すると `build_go` が `return 1` するため、
`set -e` がその時点でスクリプトを終了させ、
`main` の末尾にある総合判定ブロック（167-175 行）に**到達しない**。

実測（意図的に失敗するテストを一時追加して計測）:

- 終了コード: **1**（正しい）
- `Build pipeline FAILED (Ns)` の出力: **無し**
- 経過時間の表示: **無し**

終了コード自体は正しいため CI では機能するが、人間が読むログとしては
成功時と失敗時で書式が非対称であり、`FAILED` の文字列で grep しても引っかからない。

### 5. feature 間の fail-fast に退避手段がない

`build_go` は feature ごとのループ内で `return 1` するため、
最初の feature が失敗すると**後続の feature は一切検証されない**。
実測でも `neurom` の失敗により `stats` はビルドもテストもされなかった。

### 6. 静的解析が一切ゲートに入っていない

`scripts/` 配下を全文検索した結果、以下はどこにも存在しない。

- `go vet`
- `gofmt` による整形チェック
- `-race`（レースディテクタ）
- `-cover`（カバレッジ計測）

### 7. レースディテクタが実際のデータ競合を検出する

本仕様の調査で `-race` を試行した結果を記録する。

| 対象 | 通常 | `-race` | 結果 |
| :--- | :--- | :--- | :--- |
| `internal/modules/vram` | 約 1 秒 | 約 42 秒 | PASS |
| `internal/bus` | 約 5 秒 | 約 6 秒 | PASS |
| `integration` | 約 41 秒 | 約 41 秒（実時間 55 秒） | **FAIL: データ競合を検出** |

検出された競合は `TestMultiCoreBlendModes` と `TestPageSizeIntegration` の 2 件で、
いずれも**製品コード側の実在の不具合**である。
詳細と修正方針は仕様 031 に分離した。

> [!IMPORTANT]
> したがって `-race` をゲートに追加する要件（R8）は**仕様 031 の完了が前提**である。

### 8. 同期が `time.Sleep` に依存している

`features/neurom/integration/` 配下に `time.Sleep` が **43 箇所**存在する。

| ファイル | 件数 |
| :--- | :--- |
| `vram_page_test.go` | 12 |
| `vram_multicore_test.go` | 9 |
| `stats_http_test.go` | 8 |
| `vram_enhancement_test.go` | 7 |
| `palette_test.go` | 3 |
| `shutdown_test.go` | 2 |
| `vram_stats_test.go` | 1 |
| `bus_panic_guard_test.go` | 1 |

固定時間待機は遅いマシンで偽陰性（フレーキー）になり、速いマシンでは時間を浪費する。
統合テストが 41 秒かかる主因でもある。

### 9. `gofmt` ゲートは行末の扱いを先に決める必要がある

`gofmt -l` を実行すると、本作業で触っていないファイルを含め**ほぼ全ファイルが列挙される**。
リポジトリの作業コピーが CRLF、git の格納が LF であるためで、
`gofmt` は CRLF を未整形と判定する。

したがって `gofmt` を素朴にゲート化すると初日から全滅する。
`.gitattributes` による行末の明示が前提となる。

### 10. CI が存在しない

`.github/workflows/` は存在しない。
検証は開発者が手元でスクリプトを実行することに完全に依存している。

## 要件

### 必須要件

| ID | 要件 |
| :--- | :--- |
| R1 | `integration_test.sh` が実在する統合テスト（`features/*/integration/`）を実行すること。対象が 0 件の場合は**成功ではなく警告付きの失敗**として扱えるオプションを設けること |
| R2 | 検証階層を分離すること。`build.sh` は単体テストのみを実行し、統合テストを実行しないこと。除外条件はディレクトリ名 `tests` ではなく実在の配置に基づくこと |
| R3 | `build.sh` の所要時間が統合テストの分だけ短縮されること（現状約 50 秒 → 単体テストのみで 10 秒台を目標とする） |
| R4 | `integration_test.sh` が `--categories` を受け付けること。カテゴリは Neurom の実態に基づいて定義すること |
| R5 | `create-specification` スキルが指示するカテゴリ名を実装と一致させること。変更は `prompts/manifest/code_content/procedures/create-specification.md` を編集し `tt prompt update` で反映すること（`.cursor/` 等を直接編集しないこと） |
| R6 | `build.sh` / `integration_test.sh` が失敗時にも総合判定行と経過時間を出力すること。終了コードは 1 を維持すること |
| R7 | `build.sh` に `go vet` のゲートを追加すること。失敗時はどの feature のどのパッケージかが判別できること |

### 任意要件

| ID | 要件 | 備考 |
| :--- | :--- | :--- |
| R8 | `-race` を実行する検証階層を設けること | **仕様 031 の完了が前提。** 所要時間が数十倍になるため既定の階層には入れない |
| R9 | feature 間の fail-fast に `--keep-going` の退避手段を設けること | 失敗した feature を一覧で報告する |
| R10 | `.gitattributes` で Go ソースの行末を確定させ、`gofmt` ゲートを追加すること | R9 より先に行末を決めないとゲート化できない |
| R11 | 統合テストの `time.Sleep` をイベント駆動の待機ヘルパに置換すること | 43 箇所。段階的に実施してよい |
| R12 | カバレッジ計測を追加すること | 閾値によるゲート化までは求めない |
| R13 | CI ワークフローを追加すること | 本仕様では階層の定義までを担保し、CI 化は別途判断する |

## 実現方針

### 検証階層の定義

3 階層に整理する。

| 階層 | スクリプト | 内容 | 目標時間 | 用途 |
| :--- | :--- | :--- | :--- | :--- |
| 1. 高速 | `build.sh` | ビルド + 単体テスト + `go vet` | 10 秒台 | 編集ごと |
| 2. 統合 | `integration_test.sh` | 統合テスト（カテゴリ指定可） | 1 分程度 | コミット前 |
| 3. 徹底 | `integration_test.sh --race` | 統合テスト + レースディテクタ | 数分 | Nightly / リリース前 |

### 統合テストの発見方法

`tests/go.mod` という存在しない前提を捨て、以下の規約に切り替える。

- 統合テストは `features/{feature}/integration/` に置く
- `integration_test.sh` は `features/*/go.mod` を持つ feature を列挙し、
  その配下の `./integration/...` を実行する
- `build.sh` は `go list ./... | grep -v '/integration$'` で統合テストを除外する

この規約は現状の配置と一致しているため、テストコードの移動は不要である。

> [!NOTE]
> **設計判断（要確認）**: 統合テストの配置規約を `integration/` とするか、
> 従来の想定どおり `tests/` に移動するかは選択の余地がある。
> 本仕様は**既存の配置を正とし、スクリプトを実態に合わせる**方針を採る。
> テストコードを移動するよりスクリプトを直す方が影響範囲が小さく、
> `build.sh` が統合テストを実行してしまう問題も同時に解消できるためである。

### カテゴリの定義

Neurom の統合テストの実態に基づき、以下を提案する。

| カテゴリ | 対象ファイル |
| :--- | :--- |
| `vram` | `vram_page_test.go`, `vram_enhancement_test.go`, `vram_multicore_test.go` |
| `monitor` | `vram_monitor_test.go` |
| `bus` | `bus_panic_guard_test.go` |
| `stats` | `stats_http_test.go`, `vram_stats_test.go` |
| `lifecycle` | `shutdown_test.go` |
| `palette` | `palette_test.go` |

実現手段は Go のビルドタグではなく**ファイル名に基づく `-run` の生成**を推奨する。
ビルドタグはファイル冒頭への記述が必要で既存テストの改変を伴うが、
カテゴリからテスト名の正規表現を組み立てる方式ならスクリプト側だけで完結する。

> [!NOTE]
> **設計判断（要確認）**: カテゴリの粒度。上記は 6 分類だが、
> `vram` / `bus` / `stats` の 3 分類に粗くまとめる案もある。
> 分類が細かいほど絞り込みは効くが、新規テスト追加時の分類漏れが起きやすい。

### `build.sh` の終了処理

`set -e` による暗黙終了を避け、`build_go` の失敗を明示的に受け止める。

```bash
# Before（161 行）
build_go

# After
build_go || true   # FAILED フラグで判定するため、ここでは終了させない
```

`build_go` 内の `return 1` は維持し、`FAILED=true` の設定順序を保証する。
これにより総合判定ブロックに必ず到達する。

## 検証シナリオ

### シナリオ 1: 統合テストランナーが実効であることの確認

1. `scripts/process/integration_test.sh` を実行する
2. `features/neurom/integration/` のテストが実際に実行され、テスト名がログに出力されることを確認する
3. 終了コードが 0 であることを確認する
4. 統合テスト内の任意の 1 件を意図的に失敗させる
5. 再実行し、**終了コードが 1** になり、総合判定行に `FAILED` と経過時間が出ることを確認する
6. 意図的な失敗を戻す

### シナリオ 2: 階層分離による高速化の確認

1. `scripts/process/build.sh` を実行し、経過時間を記録する
2. ログに `integration` パッケージのテストが**現れないこと**を確認する
3. 経過時間が現状の約 50 秒から短縮されていることを確認する
4. `scripts/process/integration_test.sh` を実行し、`integration` パッケージが実行されることを確認する
5. 両者を合計しても現状と同程度以内であることを確認する（二重実行になっていないこと）

### シナリオ 3: カテゴリ絞り込みの確認

1. `scripts/process/integration_test.sh --categories "vram"` を実行する
2. `vram_*` のテストのみが実行され、`stats_http_test.go` のテストが実行されないことを確認する
3. `scripts/process/integration_test.sh --categories "vram,stats"` を実行し、両方が実行されることを確認する
4. `scripts/process/integration_test.sh --categories "存在しない名前"` を実行し、
   **黙って 0 件成功にならず**、エラーとして終了コード 1 になることを確認する

### シナリオ 4: 静的解析ゲートの確認

1. `scripts/process/build.sh` を実行し、`go vet` が実行されていることをログで確認する
2. 任意のファイルに `go vet` が検出する誤り（例: `fmt.Printf` の書式指定子の不一致）を一時的に加える
3. `build.sh` が終了コード 1 で失敗し、該当 feature とパッケージが特定できることを確認する
4. 一時的な誤りを戻す

### シナリオ 5: スキルとの整合の確認

1. `prompts/manifest/code_content/procedures/create-specification.md` のカテゴリ記述を更新する
2. `tt prompt update` を実行する
3. `.cursor/`, `.claude/`, `.codex/`, `.agent/` 配下の `create-specification` に
   更新後のカテゴリが反映されていることを確認する
4. 反映後のスキルに記載されたコマンドをそのまま実行し、成功することを確認する

## テスト項目

本仕様の対象はテスト基盤そのものであるため、
「スクリプトの振る舞いを検証する」という構造になる。

| 要件 | 検証手段 | 実行コマンド |
| :--- | :--- | :--- |
| R1: ランナーの実効化 | シナリオ 1 | `scripts/process/integration_test.sh` |
| R2: 階層分離 | シナリオ 2。`build.sh` のログに `integration` が出ないこと | `scripts/process/build.sh` |
| R3: 高速化 | シナリオ 2 の経過時間比較 | `scripts/process/build.sh` |
| R4: カテゴリ | シナリオ 3 | `scripts/process/integration_test.sh --categories "vram"` |
| R5: スキルの整合 | シナリオ 5 | `tt prompt update` 後に反映内容を確認 |
| R6: 失敗時の総合判定 | シナリオ 1 の手順 4-5、シナリオ 4 の手順 2-3 | 意図的な失敗を注入して終了コードと出力を確認 |
| R7: `go vet` | シナリオ 4 | `scripts/process/build.sh` |
| R8: `-race`（任意） | 仕様 031 完了後に実行し PASS すること | `scripts/process/integration_test.sh --race` |

### ビルド・全体検証

1. ビルド + 単体テスト + `go vet`:

   ```bash
   scripts/process/build.sh
   ```

2. 統合テスト（VRAM 系のリグレッション確認）:

   ```bash
   scripts/process/integration_test.sh --categories "vram"
   ```

3. 統合テスト（統計とライフサイクルの確認）:

   ```bash
   scripts/process/integration_test.sh --categories "stats,lifecycle"
   ```

4. 全カテゴリ（コミット前の最終確認）:

   ```bash
   scripts/process/integration_test.sh
   ```

> [!WARNING]
> 上記コマンドのうち `--categories` は**本仕様の実装後に初めて使用可能**になる。
> 本仕様より前に書かれた仕様書（024 から 029）の検証コマンドは
> `--categories` を含んでいるため実行できない。
> 実装時にそれらの記述も修正するか、少なくとも実行不能である旨を注記すること。

## 関連仕様

| 仕様 | 関係 |
| :--- | :--- |
| 031 | 本調査で検出したデータ競合の修正。R8（`-race` ゲート）の前提 |
| 024 | 本問題の発見契機。`arcproto` 抽出時に `build.sh` の所要時間と `integration_test.sh` の無効性が露見した |
| 025 から 029 | 検証コマンドに実行不能な `--categories` を含む。本仕様の実装時に整合を取る |
