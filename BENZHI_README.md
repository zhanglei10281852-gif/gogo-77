# BENZHI_README

## 项目说明

- 项目：zhanglei10281852-gif/gogo-77
- 项目用途：An offline backend command line tool for environmental H5 surveillance signal processing and alerting. It takes assay results measured from wastewater influent, waterway grab samples and sediment, together with wild-bird carcass observations, and turns them into quantified concentrations, quality verdicts, normalised loads, rolling baselines, exceedance and trend findings, catchment roll-ups, corroborated graded signals and a persistent alert lifecycle.
- Go 工具链：`golang:1.22`
- 前端工具链：无

## 标准构建、运行和测试命令

进入容器后执行：

```bash
# 编译
cd '/app' && GOTOOLCHAIN=local go build ./...

# 启动
cd '/app' && GOTOOLCHAIN=local go run ./cmd/fluwatershed

# 测试
cd '/app' && GOTOOLCHAIN=local go test ./...
```

## Docker 构建和进入容器

```bash
chmod +x build_benzhi_docker.sh
./build_benzhi_docker.sh benzhi-task-77-amd64 linux/amd64
./build_benzhi_docker.sh benzhi-task-77-arm64 linux/arm64
docker run -it benzhi-task-77-amd64:latest
docker run -it --platform linux/arm64 benzhi-task-77-arm64:latest
```

## 题目验证命令

1. 预期退出码 0：`go test ./internal/trend -run "^TestTheilSenLeavesItsInputSlicesUntouched$" -count=1 -v`
2. 预期退出码 0：`go test -buildvcs=false -count=1 ./...`
3. 预期退出码 0：`go build ./... && go vet ./...`

## Bug 复现

Bug 现象、触发步骤和完整错误信息见 `BUG_REPRO.md`。
