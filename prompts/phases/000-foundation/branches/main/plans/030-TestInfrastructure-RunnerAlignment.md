# 030-TestInfrastructure-RunnerAlignment

> **Source Specification**: [030-TestInfrastructure-RunnerAlignment.md](../ideas/030-TestInfrastructure-RunnerAlignment.md)

## Goal Description

テスト基盤の意図と実態を一致させる。

現状 `integration_test.sh` は存在しない `tests/go.mod` を前提として常に何も実行せず終了コード 0 で成功し、
一方 `build.sh` は `features/*/integration/` を除外できずに単体テストとして実行している
（所要時間約 50 秒のうち約 41 秒が統合テスト）。
加えて `create-specification` 等が指示する `--categories` は未実装で、
存在しない `build.sh` フラグもルールに書かれたままである。

本計画では以下を実現する。

1. `build.sh` を単体テスト + `go vet` の高速階層に戻す
2. `integration_test.sh` を実在の統合テストの実行器にし、`--categories` / `--race` / `--require-tests` を実装する
3. Agent 向けマニフェスト（スキル・ルール）を Neurom の実態に合わせる
4. 失敗時にも総合判定行と経過時間を必ず出力する

仕様 031 は完了済みのため、任意要件 R8（`--race`）も本計画の実施対象に含める。

## User Review Required

### 1. 任意要件のスコープ

| 要件 | 本計画での扱い | 理由 |
| :--- | :--- | :--- |
| R8 (`--race`) | **実施する** | 仕様 031 完了により前提が満たされた |
| R9 (`--keep-going`) | **実施する** | 変更が小さく、fail-fast の退避として有用 |
| R10 (`.gitattributes` + `gofmt`) | **先送り** | 行末の一斉変更が巨大な diff になり、本計画の主目的を覆い隠す。別仕様とする |
| R11 (`time.Sleep` 置換) | **先送り** | 43 箇所のテスト書き換えは別仕様の規模。ランナー整備とは独立 |
| R12（カバレッジ） | **先送り** | 本計画では階層定義まで。計測自体は後続 |
| R13（CI） | **先送り** | 仕様どおり「階層の定義までを担保し、CI 化は別途判断」 |

### 2. カテゴリ絞り込みの実装手段

ファイル名からテスト関数名を抽出し `-run` 正規表現を組み立てる方式を採る
（ビルドタグは既存テストの改変が必要なため採用しない）。

> [!NOTE]
> `TestVRAMAccessorMonitorIntegration` はファイル `vram_page_test.go` にあり
> カテゴリは `vram` である。テスト名に `Monitor` が含まれるため、
> **ファイル単位の対応表**を正とし、名前の推測では分類しない。

## Requirement Traceability

| Requirement (from Spec) | Implementation Point |
| :--- | :--- |
| R1: ランナーの実効化 + 0 件時の扱い | Proposed Changes > `integration_test.sh`。`--require-tests` で 0 件を失敗にできる |
| R2: 階層分離 | Proposed Changes > `build.sh` の除外を `/integration` に変更 |
| R3: 高速化 | Verification Plan > シナリオ 2。目標 10 秒台 |
| R4: `--categories` + 未分類ファイル検出 | Proposed Changes > カテゴリ対応表と未分類チェック |
| R5: スキル・ルールの整合 | Proposed Changes > `prompts/manifest/code_content/` + `tt prompt update` |
| R6: 失敗時の総合判定 | Proposed Changes > 両スクリプトの `main` 終了処理 |
| R7: `go vet` | Proposed Changes > `build.sh` に vet ステップ追加 |
| R8: `--race` | Proposed Changes > `integration_test.sh --race`（031 完了済み） |
| R9: `--keep-going` | Proposed Changes > `build.sh --keep-going` |
| R10–R13 | 先送り（User Review Required 参照） |

### 024–029 の検証コマンド注記

仕様の WARNING どおり、024–029 の検証コマンドは本実装前は実行不能だった。
本計画では **スキル・ルールを直すこと**を主とし、過去仕様書 024–029 の本文は
「実装後に使えるようになる」旨の注記を各 ideas の Verification 節へ 1 行追加するに留める
（全文書き換えは範囲外。実装後のコマンドが動くことが本質的な是正である）。

## Proposed Changes

### カテゴリ対応表（スクリプト内の正典）

両スクリプトから参照する対応を `integration_test.sh` 内に定数として持つ。

| カテゴリ | 対象ファイル | 含まれるテスト関数（参考） |
| :--- | :--- | :--- |
| `vram` | `vram_page_test.go`, `vram_enhancement_test.go`, `vram_multicore_test.go` | `TestPage*`, `TestDemoProgram`, `TestBlitAndReadRect`, `TestAlphaBlendPipeline`, `TestMultiCore*`, `TestVRAMAccessorMonitorIntegration` |
| `monitor` | `vram_monitor_test.go` | `TestVRAMMonitorIntegration` |
| `bus` | `bus_panic_guard_test.go` | `TestBusPanicGuard_*` |
| `stats` | `stats_http_test.go`, `vram_stats_test.go` | `TestHTTPStats*`, `TestVRAMStatsIntegration` |
| `lifecycle` | `shutdown_test.go` | `TestGracefulShutdownViaBus` |
| `palette` | `palette_test.go` | `TestPaletteUpdate` |

未分類検出: `features/*/integration/*_test.go` を列挙し、上記表のいずれにも無いファイルがあれば
`--categories` の有無にかかわらず **終了コード 1** で失敗する（R4）。

### scripts/process

#### [MODIFY] [scripts/process/build.sh](scripts/process/build.sh)

*   **Description**: 統合テスト除外、`go vet`、失敗時総合判定、`--keep-going`
*   **Technical Design**:

    引数:

    | フラグ | 意味 |
    | :--- | :--- |
    | `--help` | 既存どおり |
    | `--keep-going` | feature 失敗後も後続 feature を続行し、最後に失敗一覧を出して終了コード 1（R9） |

    ヘッダコメントから存在しない `--backend-only` の言及を削除する。

*   **Logic**:

    1. **除外フィルタの置換**（115 行付近）:

        ```bash
        # Before
        UNIT_PKGS=$(go list ./... | grep -v '/tests/' | grep -v '/tests$' || true)

        # After
        UNIT_PKGS=$(go list ./... | grep -v '/integration$' | grep -v '/integration/' || true)
        ```

        `features/neurom/integration` はパッケージパス末尾が `integration` のため
        `/integration$` で落ちる。将来の `.../integration/foo` も `/integration/` で除外する。

    2. **`go vet` ゲート**（単体テスト成功後、ビルド前）:

        ```bash
        info "Running go vet for $feature_name..."
        if ! echo "$UNIT_PKGS" | xargs go vet; then
            fail "go vet failed for $feature_name."
            FAILED=true
            FAILED_FEATURES+=("$feature_name (vet)")
            if [[ "$KEEP_GOING" != "true" ]]; then
                return 1
            fi
            continue
        fi
        success "go vet passed for $feature_name."
        ```

        `UNIT_PKGS` が空のときは vet をスキップし警告のみ出す。

    3. **失敗時の総合判定**（`main`）:

        ```bash
        build_go || true

        local elapsed=$(( SECONDS - start_time ))
        echo ""
        echo -e "${BOLD}─────────────────────────────────────────────${NC}"

        if [[ "$FAILED" == "true" ]]; then
            fail "Build pipeline FAILED (${elapsed}s)"
            if [[ ${#FAILED_FEATURES[@]} -gt 0 ]]; then
                echo -e "${RED}Failed features:${NC}"
                for f in "${FAILED_FEATURES[@]}"; do
                    echo -e "  - $f"
                done
            fi
            echo -e "${RED}Fix the errors above before running integration tests.${NC}"
            exit 1
        else
            success "Build pipeline PASSED (${elapsed}s)"
            echo -e "${GREEN}Ready for integration tests: ./scripts/process/integration_test.sh${NC}"
            exit 0
        fi
        ```

        `build_go` 内の単体テスト／ビルド失敗時も `FAILED=true` と
        `FAILED_FEATURES+=(...)` を設定してから、`--keep-going` でなければ `return 1`。
        `set -e` 下でも `build_go || true` により総合判定ブロックへ必ず到達する（R6）。

#### [MODIFY] [scripts/process/integration_test.sh](scripts/process/integration_test.sh)

*   **Description**: 実在する統合テストを実行するランナーへ全面書き換え
*   **Technical Design**:

    引数:

    | フラグ | 意味 |
    | :--- | :--- |
    | `--categories <list>` | カンマ区切り。例: `vram,stats`。未指定なら全カテゴリ |
    | `--specify <Filter>` | 既存どおり `go test -run` に渡す。カテゴリ由来の正規表現と AND（両方指定時はカテゴリ∩specify） |
    | `--race` | `go test -race` を付与（R8） |
    | `--require-tests` | 実行対象が 0 件なら終了コード 1（R1）。既定は警告して 0（後方互換の開発用）。CI 相当では付ける |
    | `--help` | ヘルプ |

*   **Logic**:

    1. **`tests/go.mod` 前提を削除**する。106–116 行の早期成功を撤去する。

    2. **feature 列挙**:

        ```bash
        for feature_dir in features/*/; do
            [[ -f "$feature_dir/go.mod" ]] || continue
            [[ -d "$feature_dir/integration" ]] || continue
            # run tests in $feature_dir/integration
        done
        ```

        `integration/` を持つ feature が 1 つも無いとき:
        *   `--require-tests` あり → `FAILED` + 終了 1
        *   なし → 警告 + 終了 0

    3. **カテゴリ対応表**（bash 連想配列）:

        ```bash
        declare -A CATEGORY_FILES=(
            [vram]="vram_page_test.go vram_enhancement_test.go vram_multicore_test.go"
            [monitor]="vram_monitor_test.go"
            [bus]="bus_panic_guard_test.go"
            [stats]="stats_http_test.go vram_stats_test.go"
            [lifecycle]="shutdown_test.go"
            [palette]="palette_test.go"
        )
        ```

    4. **未分類ファイル検出**（各 feature の `integration/` に対して）:

        ```bash
        # all *_test.go must appear in some CATEGORY_FILES value
        # if not: fail "Unclassified integration test file: $f" ; exit 1
        ```

    5. **未知カテゴリ**:

        ```bash
        # if user passed --categories "foo" and foo not in keys:
        fail "Unknown category: foo (allowed: vram,monitor,bus,stats,lifecycle,palette)"
        exit 1
        ```

        黙って 0 件成功にはしない（シナリオ 3）。

    6. **`-run` の組み立て**:

        *   選択カテゴリのファイル一覧を得る
        *   各ファイルから `^func Test` を grep し、関数名を `|` で連結
        *   `--specify` がある場合:
            *   カテゴリ由来 regex を `A`、specify を `B` として
              `-run "^(?:(?:A)$)` ではなく、Go の `-run` は部分一致なので
              **実運用は** `-run "B"` のみ指定し、カテゴリは
              **実行するファイル集合の制限**ではなく
              **抽出したテスト名の和集合**で絞る
            *   両方あるときは `-run "(A).*(B)|(B).*(A)"` のような交差は脆いため、
              **カテゴリで得た関数名リストをさらに `grep -E "$SPECIFY"` で絞り込む**

        擬似コード:

        ```bash
        names=()
        for f in $selected_files; do
            while read -r fn; do names+=("$fn"); done < <(grep -oE '^func Test[A-Za-z0-9_]+' "$f" | sed 's/^func //')
        done
        if [[ -n "$SPECIFY" ]]; then
            filtered=()
            for n in "${names[@]}"; do
                [[ "$n" =~ $SPECIFY ]] && filtered+=("$n")
            done
            names=("${filtered[@]}")
        fi
        if [[ ${#names[@]} -eq 0 ]]; then
            # handle empty: --require-tests → fail, else warn+skip feature
        fi
        run_regex=$(IFS='|'; echo "${names[*]}")
        go_test_args+=("-run" "^($run_regex)$")
        ```

    7. **実行**:

        ```bash
        cd "$PROJECT_ROOT/$feature_dir"
        go_test_args=("-v" "-count=1")
        [[ "$RACE" == "true" ]] && go_test_args+=("-race")
        go_test_args+=("-run" "^($run_regex)$" "./integration/")
        go test "${go_test_args[@]}"
        ```

    8. **失敗時総合判定**: `build.sh` と同様、`set -e` でも最終ブロックに到達する構造にする。

        ```bash
        run_all_features || true
        # print PASS/FAILED + elapsed; exit 0/1
        ```

### Agent マニフェスト（R5）

正典は `prompts/manifest/code_content/`。`.cursor/` 等は直接編集しない。
変更後に `./scripts/code/prompt/update.sh`（または `tt prompt update --target all`）で反映する。

#### [MODIFY] [prompts/manifest/code_content/procedures/create-specification.md](prompts/manifest/code_content/procedures/create-specification.md)

*   **更新内容**:
    *   カテゴリ一覧を `vram`, `monitor`, `bus`, `stats`, `lifecycle`, `palette` に置換
    *   記述例を Neurom 向けに置換:

        ```text
        scripts/process/build.sh
        scripts/process/integration_test.sh --categories "vram"
        scripts/process/integration_test.sh --categories "stats,lifecycle"
        ```

    *   `common` / `llm` / `taskengine` / `template` / `gui` の記述を削除

#### [MODIFY] [prompts/manifest/code_content/policies/planning-rules.md](prompts/manifest/code_content/policies/planning-rules.md)

*   **更新内容**:
    *   Unit 実行を `scripts/process/build.sh`（フラグなし）に修正。`--skip-frontend` / `--skip-etc` / `--backend-only` を削除
    *   統合テスト配置を `features/{feature}/integration/` に修正
    *   E2E / GUI / `tests/` 前提のうち、Neurom に無い検証コマンド例を
      `integration_test.sh --categories "vram"` 系へ置換
    *   「生の `go test` 禁止」は維持するが、例外として
      「`-race` は `integration_test.sh --race` を使う」と明記（031 計画の逸脱を解消）

#### [MODIFY] [prompts/manifest/code_content/policies/testing-rules.md](prompts/manifest/code_content/policies/testing-rules.md)

*   **更新内容**:
    *   テスト実行マトリクスを Neurom 実態に合わせて書き換え:

        | Component | Level | Command |
        | :--- | :--- | :--- |
        | Backend (Go) | Unit | `scripts/process/build.sh` |
        | Backend (Go) | Integration | `scripts/process/integration_test.sh --categories "..."` |
        | Backend (Go) | Race | `scripts/process/integration_test.sh --race` |

    *   統合テスト配置: `features/{feature}/integration/`
    *   GUI / Playwright / xvfb / Docker / syslogd の記述は
      「本リポジトリでは未使用」と注記するか、検証コマンド例から除去する
      （無関係な長文の全面削除は範囲外。**実行を義務付ける箇所だけ**直す）

#### [MODIFY] [prompts/manifest/code_content/policies/coding-rules.md](prompts/manifest/code_content/policies/coding-rules.md)

*   **更新内容**: Unit/Integration の実行コマンドと配置を同上に合わせる
      （`--skip-frontend --skip-etc` と `tests/` 配置の記述を修正）

#### [MODIFY] 関連 procedure（最小限）

以下もカテゴリ例が旧プロジェクトのままなので、例示を Neurom カテゴリに置換する。

*   `procedures/create-implementation-plan.md` — Verification 例の `--categories gui` 等
*   `procedures/execute-implementation-plan.md` — 同上
*   `procedures/build-pipeline.md` — 同上
*   `procedures/run-all-tests.md` — `features/backend/tests` 前提を `features/*/integration` に
*   `procedures/test-generator.md` — カテゴリ例のみ

### 過去仕様書への注記（最小）

#### [MODIFY] ideas 025–029（各 1 行）

*   **更新内容**: Verification / テスト項目の節末に以下を追加:

    ```markdown
    > [!NOTE]
    > `--categories` は仕様 030 実装後に使用可能。それ以前は実行不能だった。
    ```

    024 は既に実装済みのため、計画書の検証節が古い場合のみ同様の注記を足す。

### ドキュメント

#### [MODIFY] 該当があれば [features/README.md](features/README.md)

*   **更新内容**: テスト実行方法が書かれていれば新コマンドに更新する。無ければ変更しない。

## Step-by-Step Implementation Guide

1.  **`build.sh` の階層分離と失敗時判定（R2, R3, R6）**:
    *   除外フィルタを `/integration` 系に変更する。
    *   `build_go || true` と `FAILED_FEATURES` を導入し、失敗時も総合判定を出す。
    *   `./scripts/process/build.sh` を実行し、ログに `integration` パッケージが出ないこと、
      所要時間が短縮されていることを確認する（目標 10 秒台）。

2.  **`build.sh` に `go vet` と `--keep-going`（R7, R9）**:
    *   vet ステップを追加する。
    *   `--keep-going` を実装する。
    *   意図的に `fmt.Printf("%d", "x")` のような vet 違反を一時追加し、
      終了コード 1 と feature 名が出ることを確認してから戻す。

3.  **`integration_test.sh` の実効化（R1, R4, R6, R8）**:
    *   `tests/go.mod` 前提を削除し、`features/*/integration/` を実行する。
    *   カテゴリ対応表、未分類検出、未知カテゴリエラー、`--specify` 交差、
      `--race`、`--require-tests`、失敗時総合判定を実装する。
    *   `./scripts/process/integration_test.sh --categories "vram"` で
      VRAM 系のみ走ることを確認する。
    *   `./scripts/process/integration_test.sh --categories "nope"` が終了 1 であること。
    *   `./scripts/process/integration_test.sh --race --categories "vram"` が PASS すること
      （031 完了済み）。

4.  **マニフェスト更新とデプロイ（R5）**:
    *   `prompts/manifest/code_content/` の該当ファイルを編集する。
    *   `./scripts/code/prompt/update.sh` を実行する（全ターゲット）。
    *   `.cursor/skills/create-specification/SKILL.md` 等に新カテゴリが反映されたことを確認する。
    *   反映後の記述どおり `./scripts/process/integration_test.sh --categories "vram"` が成功すること。

5.  **過去仕様への注記**:
    *   ideas 025–029（必要なら 024）に 1 行 NOTE を追加する。

6.  **Verification Plan を実行**し、総合判定を計画書末尾に追記する。

## Verification Plan

### Automated Verification

1.  **Build & Unit Tests（階層 1）**:

    ```bash
    ./scripts/process/build.sh
    ```

    *   **Log Verification**:
        *   `integration` パッケージの `=== RUN` が現れない
        *   `go vet` のログが出る
        *   `Build pipeline PASSED (Ns)` で N がおおよそ 10 秒台
        *   終了コード 0

2.  **Integration Tests（階層 2・カテゴリ）**:

    ```bash
    ./scripts/process/build.sh && ./scripts/process/integration_test.sh --categories "vram"
    ```

    *   **Log Verification**: `TestPage*`, `TestMultiCore*`, `TestDemoProgram` 等が実行される。
      `TestHTTPStats*` / `TestGracefulShutdownViaBus` は実行されない。
    *   終了コード 0

3.  **複数カテゴリと未知カテゴリ**:

    ```bash
    ./scripts/process/integration_test.sh --categories "vram,stats"
    ./scripts/process/integration_test.sh --categories "does-not-exist"; echo EXIT:$?
    ```

    *   前者 PASS。後者は終了コード 1 で `Unknown category` を含む。

4.  **全統合テスト**:

    ```bash
    ./scripts/process/integration_test.sh --require-tests
    ```

    *   全カテゴリが実行され PASS。終了コード 0。

5.  **Race（階層 3）**:

    ```bash
    ./scripts/process/integration_test.sh --race --categories "vram,monitor"
    ```

    *   `DATA RACE` が 0 件。終了コード 0。

6.  **失敗時総合判定（R6）**:
    *   一時的に統合テスト 1 件を `t.Fatal` で壊し、
      `./scripts/process/integration_test.sh --categories "palette"` を実行する。
    *   終了コード 1、`FAILED` 行、経過秒が出ることを確認してから戻す。
    *   同様に単体テスト側でも一時失敗を入れ、`build.sh` が
      `Build pipeline FAILED (Ns)` を出すことを確認してから戻す。

7.  **マニフェスト反映（R5）**:

    ```bash
    ./scripts/code/prompt/update.sh
    ```

    *   `.cursor/` / `.claude/` 配下の `create-specification` に
      `vram|monitor|bus|stats|lifecycle|palette` が含まれること。
    *   旧カテゴリ `common`, `llm`, `gui` が検証コマンド例から消えていること。

### E2E Tests

本計画では `tests/` 配下の E2E を追加しない。理由:

*   変更対象はシェルランナーと Agent マニフェストであり、アプリケーションの外部振る舞いを変えない
*   検証は上記のスクリプト実行そのものが自動化された受け入れテストになる
*   製品コードの E2E インフラ（`tests/agentservice_e2e_test.go` 等）は本リポジトリに存在しない

### テスト項目設計のセルフレビュー（testing-rules §11.4）

#### ボトムアップ順序

| Step | 対象 | 確認内容 |
| :--- | :--- | :--- |
| 1 | `build.sh` 除外フィルタ | 単体のみ・高速 |
| 2 | `build.sh` vet / 失敗判定 | ゲートとログ |
| 3 | `integration_test.sh` 発見と実行 | 実効化 |
| 4 | カテゴリ / 未知 / 未分類 | 絞り込みの正しさ |
| 5 | `--race` | 031 後の階層 3 |
| 6 | マニフェスト | ドキュメントと実装の一致 |

#### 観点チェックリスト

| # | 観点 | 対応 |
| :--- | :--- | :--- |
| 1 | 正常系 | 手順 1, 2, 4 |
| 2 | 異常系・境界 | 未知カテゴリ、意図的失敗、0 件 + `--require-tests` |
| 3 | 外部連携 | 実 `go test` 実行（モックしない） |
| 4 | 一貫性 | カテゴリ表と実ファイルの未分類検出 |
| 5 | 状態遷移 | 失敗注入 → FAILED 行 → 復元 |
| 6 | 設定反映 | `tt prompt update` 後のスキル内容 |
| 7 | 副作用 | 一時失敗の復元を手順に含める。ワーキングツリーを汚さない |

#### セルフレビュー結果

1. **網羅性**: R1–R9（R10–R13 除く）が手順で覆われる。先送りは明示した。
2. **証拠の十分性**: 終了コード・ログ文字列・実行されたテスト名・所要時間を見る。
3. **迂回排除**: 未分類ファイルを失敗させることで、対応表漏れで「全パス」と誤認しない。
4. **依存関係**: `build.sh` 成功を統合の前に置く（testing-rules §2）。

### 総合判定（testing-rules §12）

全手順完了後、§12.3 フォーマットで計画書末尾に追記する。
特に「`integration_test.sh` が実際にテスト名を出したか」を手順 1 のスキップ罠として確認する。

## Documentation

マニフェスト更新（上記 R5）が Agent 向けドキュメントの主更新である。
加えて:

#### [MODIFY] [prompts/specifications](prompts/specifications)（該当時のみ）

*   テスト実行手順を述べている文書があれば新コマンドへ更新。無ければ変更しない。
