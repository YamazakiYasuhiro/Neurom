---
name: build-pipeline
description: Runs full build, container setup, integration/E2E tests, and fix loops using scripts/process/build.sh and integration_test.sh. Use after code changes, when verifying the repo, or when the user asks to run the build or test pipeline.
---

# Build and Verification Pipeline

コードの変更後に安全性（テスト通過）と正当性（ビルド成功）を検証し、統合テストまで一貫して実行する。
主に `build.sh` と `integration_test.sh` を使用する。

## 1. 準備: ステータスの確認

1. `scripts/utils/show_current_status.sh` を実行する。
2. JSONフォーマットの出力から `phase` を取得し、以下 `[Phase]` として参照する。
3. ウォークスルー等の成果物パスには、このフェーズ名を使用する。

## 2. Full Build & Unit Test

プロジェクト全体（Backend, Frontend, Extension）のビルドと単体テストを一括実行する。
統合テストの前に必ずこのステップで成果物（拡張機能のバイナリやWebviewのアセット）を最新にする。

```bash
./scripts/process/build.sh
```

> **Note**: バックエンドのみを高速に確認したい場合は `./scripts/process/build.sh --skip-frontend --skip-etc` も利用可能。

## 3. Environment Setup

統合テスト（Go Integration, GUI E2E）のために必要なコンテナ環境（DB, LLM Mock等）をセットアップする。

```bash
./scripts/setup/setup_containers.sh
```

## 4. Integration & E2E Tests

全ての統合テストとE2Eテストを実行する。
デフォルトで Backend (Go) の統合テストと、VSCode拡張機能 (GUI) のE2Eテストの両方が実行される。

> [!IMPORTANT]
> **Prerequisite**: このステップの前に **Step 2: Full Build** が成功している必要がある。
> ビルドを行わずにテストを実行すると、古いバイナリやWebviewアセットに対してテストが行われる。

```bash
./scripts/process/integration_test.sh
```

### オプション実行（個別実行）

特定のカテゴリやテストのみを実行する場合（ワークフロー外の手動実行含む）:

```bash
# CLI / Backend (Go) の特定カテゴリのみ実行
./scripts/process/integration_test.sh --categories xxx

# テスト名を指定して実行 (Go/TestRunner共通)
./scripts/process/integration_test.sh --specify "TestNameRegex"

# 前回の失敗テストの次から再開
./scripts/process/integration_test.sh --resume
```

## 5. Analyze Results & Feedback Loop

テストが失敗した場合や、期待通りの動作をしなかった場合は、原因を特定し修正する。

### 5.1 レポートの確認
テストが失敗した場合、ログを確認してエラー原因を特定する。

### 5.2 デバッグと修正
1. **修正の実施**: 実装コードまたはテストコードを修正する。
2. **失敗テストのみ再実行**: 修正後、失敗したテストのみを再実行して修正が有効か確認する。
    - 例: `./scripts/process/integration_test.sh --categories xxx --specify "xxx-1"`
3. **残りのテストを再開**: 失敗テストが通過したら、`--resume` で残りのテストから再開する。
    - `./scripts/process/integration_test.sh --resume`
    - 既に通過済みのテストをスキップし、失敗テストの次から実行される。

## 6. Final Check

全てのテストが通過し、リグレッションがないことが確認できたら、タスク完了とする。
