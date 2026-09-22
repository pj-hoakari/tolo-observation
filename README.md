# tolo-observation

## 開発

### セットアップ

mise を使用して開発環境をセットアップする  
`mise install` の後処理で lefthook の pre-commit フック（シークレット検出と golangci-lint）が導入される

```bash
mise trust
mise install
cp .env.example .env
task proto
```

`.env` は Compose と `task migrate:*` が開発用 PostgreSQL の接続情報（`POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB`）を読む先で、git 管理外

### テストと lint

```bash
go build ./...
go test ./...
golangci-lint run
```

`go test ./...` は統合テストで PostgreSQL コンテナを起動するため、Docker が必要

### Docker Compose での起動

Docker Compose で PostgreSQL、golang-migrate によるマイグレーション、および開発サーバーを起動できる

```bash
docker compose up --build
```

もしくは
```bash
task up:build
```

サーバーは `http://localhost:8080`、PostgreSQL は `localhost:5432` で待ち受ける（停止は `task down`）  
Compose の `migrate` サービスは `Dockerfile` の `migrate` ターゲットをビルドして起動するため、`migrations/` を追加・変更したあとは `--build` 付き（`docker compose up --build` または `task up:build`）で起動し直す  
`task up` はイメージを再ビルドしないので、古いマイグレーションのままになる  
RPC を呼び出すには Service Gateway 発行の内部 JWT が必要なので、`go tool jwtgen` で生成した JWKS を配信する URL を `INTERNAL_JWKS_URL` で `server` に渡す（「内部 JWT」を参照）

### 環境変数

| 環境変数 | デフォルト | 内容 |
| --- | --- | --- |
| `SERVER_ADDR` | `:8080` | 待ち受けるアドレス |
| `DATABASE_URL` | なし（必須） | PostgreSQL の接続先 |
| `INTERNAL_JWKS_URL` | `http://gateway:8080/.well-known/jwks.json` | 内部 JWT の検証に使う JWKS の取得先 |
| `INTERNAL_JWT_ISSUER` | `service-gateway` | 内部 JWT に期待する `iss` |
| `INTERNAL_JWT_AUDIENCE` | `tolo-observation` | 内部 JWT に期待する `aud` |
| `LOG_LEVEL` | `info` | ログに出力する最小レベル。`debug`／`info`／`warn`（`warning` も同義）／`error`／`critical` を取り、未知の値ならサーバーは起動しない |
| `GOOGLE_CLOUD_PROJECT` | なし | 設定するとログの `logging.googleapis.com/trace` を `projects/<project>/traces/<trace_id>` 形式にし、Cloud Logging でトレースと相関させる。未設定なら素のトレース ID を出力する |
| `OTEL_EXPORTER_OTLP_ENDPOINT` / `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | なし | どちらかを設定したときだけ OTLP/HTTP で span を export する。未設定ならトレーシングは無効 |
| `OTEL_SERVICE_NAME` | `tolo-observation` | トレースの `service.name` |

ヘッダ・TLS・タイムアウトなどその他の `OTEL_EXPORTER_OTLP_*` は exporter がそのまま解釈する  
ログは標準出力へ 1 行 1 件の JSON で書き出す

### マイグレーション

ローカルでマイグレーションを実行する場合は、Compose で PostgreSQL を起動してから次を実行する（接続先は `DATABASE_URL` で上書きできる）  
`task migrate:*` が使う golang-migrate CLI は `.config/mise/conf.d/db.toml` で管理しており、`mise install` で `mise.toml` のツールと一緒に導入される

```bash
# マイグレーションを適用
task migrate:up
# 新しいマイグレーションを作成（up/down のペアを生成）
task migrate:create -- <migration_name>
# 1 つ前にロールバック
task migrate:down
# 現在のバージョンと dirty 状態を表示
task migrate:version
```

### トレースの確認（Jaeger）

監視スタックはオーバーライドファイル `compose.o11y.yml` を重ねたときだけ有効になる  
Jaeger が起動し、`server` に OTLP エクスポート用の環境変数（`OTEL_EXPORTER_OTLP_ENDPOINT` など）がセットされる

```bash
docker compose -f compose.yml -f compose.o11y.yml up --build
```

もしくは
```bash
task up:build:o11y
```

Jaeger UI は `http://localhost:16686`（停止は `task down:o11y`）

### 内部 JWT

ローカルでの動作確認には `internal-jwt-handling` 同梱の jwtgen CLI でトークンと JWKS を生成する  
`go.mod` の `tool` に登録してあるので `go tool jwtgen` で実行できる

```bash
# ES256 の内部 JWT と対応する JWKS ドキュメントを JSON で出力
go tool jwtgen -audience tolo-observation -tenant-public-id 0123456789abcdef -scope <scope> -ttl 10m
```

- `-token-use` は `tenant_access`（既定）、`event_access`、`registration`、`service` を取る
- `tenant_access` では `-tenant-public-id`（ランダムな 16 文字 hex）と `-scope` が必須である。必要な scope は proto の `required_scopes` で宣言されている
- `-issuer` の既定は `service-gateway`、`-audience` に既定はないので `tolo-observation` を明示する
- そのほかのフラグは `-event-public-id`、`-origin-sub`、`-subject`、`-txn`、`-kid`（既定 `test-key`）、`-ttl`（既定 2 分）である

出力は `token` / `claims` / `jwks` を持つ JSON である  
`jwks` を任意の HTTP エンドポイント（例: ローカルのファイルサーバ）で配信し、`INTERNAL_JWKS_URL` にその URL を設定すると、`Authorization: Bearer <token>` で呼び出せる

### connect-es の生成

connect-es の生成（`task proto:gen:es`）はリリース時に CI で行う  
ローカルで実行する場合は `clients/connect-es` の依存（`npm i`）を導入する必要がある

## イメージからマイグレーションを実行する

`Dockerfile` の `migrate` ターゲットは golang-migrate CLI のイメージに `migrations/` を `/migrations` として同梱したものである  
リポジトリを clone しなくても、このイメージだけで DB マイグレーションを実行できる  
`ENTRYPOINT` は `migrate -path /migrations` なので、利用者は接続先とコマンドだけを引数として渡す

ローカルでビルドする場合は次のとおりである

```bash
docker build --target migrate -t tolo-observation-migrate .
```

適用は `-database` に接続先を、続けてコマンドを渡す

```bash
docker run --rm --network host tolo-observation-migrate -database "$DATABASE_URL" up
```

公開イメージは `ghcr.io/<owner>/<repo>-migrate` で、サーバーのイメージと同じバージョンタグを付ける

```bash
docker run --rm --network host ghcr.io/<owner>/<repo>-migrate:<version> -database "$DATABASE_URL" up
```

そのほかのコマンドも同じ形で渡す

```bash
# 現在のバージョンを確認
docker run --rm --network host ghcr.io/<owner>/<repo>-migrate:<version> -database "$DATABASE_URL" version
# 1 つ前にロールバック
docker run --rm --network host ghcr.io/<owner>/<repo>-migrate:<version> -database "$DATABASE_URL" down 1
```

golang-migrate CLI は接続先を環境変数からは読まないため、`-database` は必ず引数で渡す  
`--network host` はコンテナからホスト上の PostgreSQL に接続するための指定であり、接続先がホスト外にあるなら不要である

## proto アーティファクトの利用

`.proto` は [ORAS](https://oras.land) で OCI アーティファクト化され、GitHub Container Registry に公開される  
アーティファクト名: `ghcr.io/<owner>/<repo>-proto`

### 取得（pull）

[ORAS CLI](https://oras.land/docs/installation) が必要（mise でも導入される）

```bash
# 出力先ディレクトリに proto を展開（ディレクトリ構造が復元される）
oras pull ghcr.io/<owner>/<repo>-proto:latest -o proto
```

取得した `.proto` は `buf` や `protoc` の入力としてそのまま利用できる
