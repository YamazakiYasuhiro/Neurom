---
description: Build, Test, and Verify Pipeline
---

# Build and Verification Workflow

このワークフローは、コードの変更後に安全性（テスト通過）と正当性（ビルド成功）を検証し、統合テストまで一貫して実行するためのものです。
スクリプトの集約により、主に `build.sh` と `integration_test.sh` を使用して検証を行います。

## 1. 準備: ステータスの確認

1.  `scripts/utils/show_current_status.sh` を実行します。
2.  JSONフォーマットの出力から `phase` を取得し、以下 `[Phase]` として参照します。
3.  ウォークスルー等の成果物パスには、このフェーズ名を使用します。

## 2. Full Build & Unit Test

プロジェクト全体（Backend, Frontend, Extension）のビルドと単体テストを一括で実行します。
統合テストを実行する前に、必ずこのステップで成果物（拡張機能のバイナリやWebviewのアセット）を最新にする必要があります。

// turbo
./scripts/process/build.sh

> **Note**: バックエンドのみを高速に確認したい場合は `./scripts/process/build.sh --backend-only` も利用可能です。

## 3. Environment Setup

統合テスト（Go Integration, GUI E2E）のために必要なコンテナ環境（DB, LLM Mock等）をセットアップします。

// turbo
./scripts/setup/setup_containers.sh

## 4. Integration & E2E Tests

全ての統合テストとE2Eテストを実行します。
デフォルトで Backend (Go) の統合テストと、VSCode拡張機能 (GUI) のE2Eテストの両方が実行されます。

> [!IMPORTANT]
> **Prerequisite**: このステップを実行する前に、必ず **Step 2: Full Build** が成功している必要があります。
> ビルドを行わずにテストを実行すると、古いバイナリやWebviewアセットに対してテストが行われ、正しい結果が得られません。

// turbo
./scripts/process/integration_test.sh

### オプション実行（個別実行）

特定のカテゴリやテストのみを実行したい場合は、以下のコマンドを使用してください（ワークフロー外で手動実行）。

```bash
# CLI / Backend (Go) の特定カテゴリのみ実行
./scripts/process/integration_test.sh --categories xxx

# テスト名を指定して実行 (Go/TestRunner共通)
./scripts/process/integration_test.sh --specify "TestNameRegex"

# 前回の失敗テストの次から再開
./scripts/process/integration_test.sh --resume
```

## 5. Analyze Results & Feedback Loop

テストが失敗した場合や、期待通りの動作をしなかった場合は、以下の手順で原因を特定し、修正を行います。

### 5.1 レポートの確認
テストが失敗した場合、ログを確認してエラー原因を特定します。

### 5.2 デバッグと修正
1.  **修正の実施**: 実装コードまたはテストコードを修正します。
2.  **失敗テストのみ再実行**: 修正後、失敗したテストのみを再実行して修正が有効か確認します。
    - 例: `./scripts/process/integration_test.sh --categories xxx --specify "xxx-1"`
3.  **残りのテストを再開**: 失敗テストが通過したら、`--resume` で残りのテストから再開します。
    - `./scripts/process/integration_test.sh --resume`
    - これにより、既に通過済みのテストをスキップし、失敗テストの次から実行されます。

## 6. Final Check

全てのテストが通過し、リグレッションがないことが確認できたら、タスク完了とします。