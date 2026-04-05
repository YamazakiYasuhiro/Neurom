---
name: execute-implementation-plan
description: Implements code and tests per an implementation plan markdown under prompts/phases/.../plans, updating checkboxes and following coding/testing rules and the build-pipeline skill. Use when the user specifies a plan file or asks to implement a plan.
---

# 実装実行

作成された実装計画書 (`.../plans/.../XXX.md`) に基づき、コーディングルールとテストルールを遵守して実装を行う。

## 1. 入力とルールの確認

1. **入力ファイルの特定**:
    * ユーザーが指定したファイル、または現在エディタで開いているファイルを「実装計画書」として扱う。
2. **ルールの読み込み**:
    * 以下のルールファイルを読み込み、作業全体を通して遵守する。
        * `prompts/rules/coding-rules.md` (コーディングルール)
        * `prompts/rules/testing-rules.md` (テスト実施ルール)

## 2. 実装の実行

1. **計画の読み込み**:
    * 実装計画書の内容を読み、変更対象のファイルや具体的な変更内容を把握する。
    * 計画が複数ファイルに分割されている場合は、すべての計画ファイルを確認する。
2. **進捗の追跡**:
    * 実装計画書内のチェックボックス `[ ]` は、作業完了時に `[x]` に変更する。
    * 進行中の項目は `[/]` に変更し、現在の作業状況を明確にする。
    * 複数ファイルにまたがる計画の場合、各ファイルのチェックボックスも適切に更新する。
3. **コーディング**:
    * 計画書の手順に従ってコードを記述・修正する。
    * **重要**: `prompts/rules/coding-rules.md` に記載されたスタイルや設計原則を厳守する。

## 3. テストと検証

### 3.1 テスト実施の順序

実装計画で定められた順序に従い、以下の順番でテストを実施する:

1. **Build & Unit Test (必須)**:
    * **全てのテストの前に**: まずプロジェクト全体をビルドし、単体テストを通過させる。統合テストが最新のバイナリとアセットに対して実行されることを保証する。
    * **実行コマンド**:
        ```bash
        ./scripts/process/build.sh
        ```
    * **重要**: このステップが失敗（Exit Code != 0）した場合、**絶対に**次のステップ（統合テスト）に進んではいけない。
2. **統合テストの実施**:
    * Step 1が成功した場合のみ、統合テストを実行する。
    * **Go統合テスト**の場合:
        * 統合テストファイルは該当カテゴリのディレクトリに配置する（例: `tests/llm/xxx_test.go`）
        * テスト実行時は、該当カテゴリを指定して実行する:
            ```bash
            ./scripts/process/integration_test.sh --categories "llm"
            ./scripts/process/integration_test.sh --categories "llm,taskengine"
            ```
    * 統合テストは actual 外部システム（LLM APIなど）との連携を確認するため、優先的に実施する。
    * **重要**: テストスクリプト (`integration_test.sh`) の **終了コード (Exit Code)** を必ず確認する。
        * Exit Codeが `0` でない場合は、**絶対に** 次のステップに進んではいけない。
        * エラーログを読み、原因を特定し、修正して再実行する「修正ループ」に入る。
3. **その他のテスト**:
    * 必要に応じて、結合テストやパフォーマンステストなども行う。

詳細なループ（`--specify`, `--resume`、コンテナセットアップの順序など）は **`build-pipeline` スキル** に従う。

### 3.2 修正と再テスト (Mandatory Fix Loop)

> [!CAUTION]
> **NEVER IGNORE FAILURES**: ビルドやテストの失敗（Exit Code != 0）を無視してタスクを完了させることは、プロジェクトへの破壊行為とみなされる。

1. **テスト失敗時の対応 (The Fix Loop)**:
    * テストやビルドが失敗した場合は、以下のループを**成功するまで**繰り返す：
        1. **Read Logs**: エラーログ、スタックトレースを読み、失敗原因を特定する。
        2. **Fix Code**: 原因を取り除くためにコードまたはテストを修正する。
        3. **Retry (失敗テストのみ)**: `--specify` で失敗テストのみを再実行して修正を確認する。
            - 例: `./scripts/process/integration_test.sh --specify "テスト名"`
        4. **Confirm (全テスト)**: 失敗テストが通過したら全テストをやりなおす。
            - `./scripts/process/integration_test.sh`
    * 「後で直す」は禁止。その場で直す。
    * 修正した内容は、実装計画書のチェックボックスや進捗状況に反映する。
2. **仕様書の更新**:
    * 実装計画に「既存仕様書の更新」が含まれている場合、実装完了後に該当する仕様書を更新する。
    * 更新内容が正確であることを確認する。
