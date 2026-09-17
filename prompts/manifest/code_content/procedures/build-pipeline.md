---
apiVersion: agent.meta/v1
id: build-pipeline
kind: procedure
title: Build, Test, and Verify Pipeline
trigger:
    command: build-pipeline
description: Run the full build, unit test, and integration test pipeline to verify code changes.
tags:
  - baseline

---

# Build and Verification Workflow

コードの変更後に安全性（テスト通過）と正当性（ビルド成功）を検証し、統合テストまで一貫して実行する。

> [!IMPORTANT]
> テスト実行の詳細ルールは `{{policy:testing-rules}}` を参照すること。

## 1. Full Build & Unit Test

プロジェクト全体のビルドと単体テスト + `go vet` を一括実行する。
統合パッケージ（`features/*/integration/`）は除外される。
統合テストは最新のビルド成果物に対して行う必要があるため、**必ずこのステップを先に通す**。

// turbo
./scripts/process/build.sh

## 2. Integration Tests

統合テストを実行する。**Step 1 が成功している必要がある。**

影響範囲に応じてカテゴリを指定する（`vram`, `monitor`, `bus`, `stats`, `lifecycle`, `palette`）。

```bash
./scripts/process/build.sh && ./scripts/process/integration_test.sh --categories "vram"
./scripts/process/integration_test.sh --categories "stats,lifecycle"
./scripts/process/integration_test.sh --require-tests
./scripts/process/integration_test.sh --race --categories "vram,monitor"
./scripts/process/integration_test.sh --specify "TestPageSize" --categories "vram"
```

## 3. Fix Loop

テストが失敗した場合は、`{{policy:testing-rules}}` Section 3「エラー修正フロー」に従い修正する。

1. エラーログを確認し原因を特定
2. コードを修正
3. `--categories` / `--specify` で失敗テストのみ再実行
4. 通過後、関連カテゴリまたは `--require-tests` で全体を再確認

## 4. Final Check & Push

全てのテストが通過し、リグレッションがないことを確認したら、遠方知識の記録のうえ `git push` する。
