---
trigger: always_on
---

# プロジェクト共通指示書 (`.agent/instructions.md`)

## 技術スタック (想定)
- 言語: Go
- アーキテクチャ: Modular/Layered Architecture

## ディレクトリ構造と情報源
- **仕様・設計**: `prompts/phases/` 配下が「正」の情報源です。
    - `000-xxx`: フェーズ名称
        - `ideas/`: 主要なアイデアメモと仕様書
            - `main/`: `main`ブランチ (現在の作業ブランチ名を入れること)
                - `000-yyy.md`: アイディアと要件を記したマークダウンファイル
                - `001-zzz.md`: 同上

## 開発プロセス

- **仕様ファースト**
    - 実装の前に `prompts/phases/...` 以下の `ideas/` フォルダに作業ブランチ名のフォルダを作成し、その下に仕様を作成・更新してください。仕様書のファイル名は、`000-` から始まる3桁の連番の数字で管理してください。

## ワークフロー間の流れ

`.agent/workflows/` 配下に定義されたワークフローは、以下の順序で連携して動作します:

### 1. 仕様書作成フェーズ
**ワークフロー**: [`create-specification.md`](.agent/workflows/create-specification.md)

1. **人間**: 実装のアイディアを考える
2. **AI**: `create-specification.md` を使って仕様のマークダウンファイルを生成
   - 出力先: `prompts/phases/000-xxx/ideas/[ブランチ名]/XXX-Name.md`
   - 内容: 背景、要件、実装計画の概要
3. **人間**: 仕様書をレビュー
   - 修正が必要な場合は、AIに指示して修正させる
   - 問題なければ、**明示的に次のフェーズへ進むよう指示する**（勝手に進まないこと）

### 2. 実装計画作成フェーズ
**ワークフロー**: [`create-implementation-plan.md`](.agent/workflows/create-implementation-plan.md)

1. **人間**: 仕様マークダウンファイルを指定
2. **AI**: `create-implementation-plan.md` を使って詳細な実装計画を作成
   - 入力: `prompts/phases/000-xxx/ideas/[ブランチ名]/XXX-Name.md`
   - 出力: `prompts/phases/000-xxx/plans/[ブランチ名]/YYY-Name.md`
   - 内容: 統合テスト計画、単体テスト計画、実装手順、検証計画
   - 大きな仕様の場合: 複数の実装計画ファイルに分割（Part1, Part2, ...）
3. **人間**: 実装計画をレビュー
   - 修正が必要な場合は、AIに指示して修正させる
   - 問題なければ、**明示的に次のフェーズへ進むよう指示する**（勝手に進まないこと）

### 3. 実装実行フェーズ
**ワークフロー**: [`execute-implementation-plan.md`](.agent/workflows/execute-implementation-plan.md)

1. **人間**: 実装計画ファイルを指定
2. **AI**: `execute-implementation-plan.md` に従って実装を実行
   - 入力: `prompts/phases/000-xxx/plans/[ブランチ名]/YYY-Name.md`
   - プロセス:
     - コーディングルール (`prompts/rules/coding-rules.md`) を遵守してコード実装
     - テストルール (`prompts/rules/testing-rules.md`) を遵守してテスト作成
     - 実装計画のチェックボックス `[ ]` → `[x]` で進捗管理
     - 進行中項目は `[/]` でマーク

### 4. ビルド・検証フェーズ
**ワークフロー**: [`build-pipeline.md`](.agent/workflows/build-pipeline.md)

実装実行フェーズ内で自動的に使用されます:

1. **AI**: `scripts/process/build.sh` を実行
   - 全体ビルドと単体テストを実行
   - 失敗時は即座に修正して再実行
2. **AI**: `scripts/process/integration_test.sh` を実行
   - 統合テストを実行
   - 失敗時は修正して該当テストのみ再実行 (`--specify` オプション使用)
3. **AI**: 必要に応じて全テストを再実行してリグレッション確認

### ワークフロー図

```
[人間: アイディア]
       ↓
[AI: create-specification.md] → 仕様書生成
       ↓
[人間: レビュー・修正・進行指示]
       ↓
[AI: create-implementation-plan.md] → 実装計画生成
       ↓
[人間: レビュー・修正・進行指示]
       ↓
[AI: execute-implementation-plan.md]
       ├→ コーディング
       ├→ テスト作成
       └→ [AI: build-pipeline.md]
              ├→ scripts/process/build.sh (全体ビルド・単体テスト)
              └→ scripts/process/integration_test.sh (統合テスト)
```

### 重要なポイント

- **人間のレビューポイント**: 仕様書と実装計画の2箇所
- **自動実行部分**: 実装実行とビルド・検証パイプライン
- **進捗管理**: 実装計画のチェックボックスで進捗を可視化
- **テスト順序**: 統合テスト → 単体テスト → その他のテスト
- **スクリプト配置**: `scripts/` ディレクトリに各種ビルド・テストスクリプトを配置
- **フェーズ移行の禁止事項**: 現在のワークフローが完了しても、人間からの明示的な指示があるまでは、勝手に次のワークフロー（フェーズ）を開始してはいけません。

## シェル環境の指定

コマンドラインでスクリプトやコマンドを実行する際は、**bash** の使用を推奨します。

### Windows環境での注意事項

> [!IMPORTANT]
> Windows環境では、PowerShellではなく必ず **Git Bash** などのbash互換環境を使用してください。

- **推奨環境**: Git Bash, WSL (Windows Subsystem for Linux), Cygwin など
- **理由**: プロジェクトのスクリプト (`scripts/` 配下) はbashスクリプトとして記述されているため、PowerShellでは正しく動作しない可能性があります
- **確認方法**: ターミナルで `bash --version` を実行して、bashが利用可能か確認してください

### Mac / Linux環境

- 標準のbashシェルを使用してください
- 特別な設定は不要です

## ファイル管理規則
- **中間生成ファイル**: タスク進行中に生成される中間ファイル（ビルドエラーログ、デバッグ出力、一時的なドキュメントなど）は、必ず `tmp/` ディレクトリ以下に作成してください。
    - 例: `build_error.txt`, `doc_delta.txt`, `doc_resp.txt` などは `tmp/build_error.txt`, `tmp/doc_delta.txt`, `tmp/doc_resp.txt` として作成
    - ログファイル、PIDファイルなども同様に `tmp/` 以下に配置すること（例: `tmp/vscode_launch.log`, `tmp/.vscode_cdp.pid`）。
    - これらのファイルはプロセスでは必要ですが、タスク完了後はゴミとなり、誤ってコミットされるリスクがあります。
    - `tmp/` ディレクトリは `.gitignore` で除外されているため、コミットされません。
- **ファイルパスの表示**: できるだけ「file://プロジェクトルートからの相対パス」を表示するようにしてください。