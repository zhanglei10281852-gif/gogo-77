# Bug Reproduction

## 包的性质

当前 test_model_fix 保存的是被测模型修复后的结果源码，不是初始含 Bug 源码。要复现原始缺陷，必须检出下面固定的 parent SHA；不要在当前修复结果源码上期待重新出现修复前失败。生成系统使用的可信验证补丁和完整验证日志仅在本地留存，不提交到结果分支。

## 问题现象

调用趋势拟合之后，我们自己那份观测数据被改掉了。传入 xs=[0,1,2,3,4]、ys=[9,1,7,3,5] 拿到斜率和截距都正常，可函数返回后再打印 ys，顺序已经变成升序排列，原始的观测顺序没了；xs 没有被动过。更明显的是拿同一对切片再调一次拟合，得到的斜率和截距和第一次不一样，因为第二次拿到的已经是被改过的数据。我们在同一批点上还要做别的统计（按天对齐、算日均、导出明细），现在必须先自己深拷贝一份才敢调用它。请修复拟合对入参的破坏，让它只读取传入的切片，同时保持斜率取两点斜率中位数、截距取残差中位数、pairs 计数、对退化输入的拒绝与既有数值结果完全不变，并保证全量测试通过。

## 含 Bug 版本

- 仓库：zhanglei10281852-gif/gogo-77
- 仓库地址：https://github.com/zhanglei10281852-gif/gogo-77.git
- parent SHA：0edb94f7f606577a32589fc48986f6b55a3525cd

## 复现步骤

```bash
git clone -- https://github.com/zhanglei10281852-gif/gogo-77.git bug-repro
cd bug-repro
git checkout --detach 0edb94f7f606577a32589fc48986f6b55a3525cd
go test ./internal/trend -run "^TestTheilSenLeavesItsInputSlicesUntouched$" -count=1 -v
```

## 双架构完整错误信息

### linux/amd64

- 容器内复现预期退出码：1
- 容器内复现实际退出码：1

stdout：

```text
$ go test ./internal/trend -run "^TestTheilSenLeavesItsInputSlicesUntouched$" -count=1 -v
=== RUN   TestTheilSenLeavesItsInputSlicesUntouched
    theilsen_input_regression_test.go:23: the caller's values were rearranged: got [2 6 9 9 9], want [9 1 7 3 5]
--- FAIL: TestTheilSenLeavesItsInputSlicesUntouched (0.00s)
FAIL
FAIL	FluWatershed/internal/trend	0.002s
FAIL

```

stderr：

```text
warning: internal/trend/theilsen_input_regression_test.go has type 100755, expected 100644
warning: internal/trend/theilsen_input_regression_test.go has type 100755, expected 100644

```

### linux/arm64

- 容器内复现预期退出码：1
- 容器内复现实际退出码：1

stdout：

```text
$ go test ./internal/trend -run "^TestTheilSenLeavesItsInputSlicesUntouched$" -count=1 -v
=== RUN   TestTheilSenLeavesItsInputSlicesUntouched
    theilsen_input_regression_test.go:23: the caller's values were rearranged: got [2 6 9 9 9], want [9 1 7 3 5]
--- FAIL: TestTheilSenLeavesItsInputSlicesUntouched (0.01s)
FAIL
FAIL	FluWatershed/internal/trend	0.157s
FAIL

```

stderr：

```text
warning: internal/trend/theilsen_input_regression_test.go has type 100755, expected 100644
warning: internal/trend/theilsen_input_regression_test.go has type 100755, expected 100644

```

## 通过条件

以 xs=[0,1,2,3,4]、ys=[9,1,7,3,5] 调用拟合后，xs 与 ys 的元素顺序和取值逐个保持不变，pairs=10；用同一对切片连续调用两次得到完全相同的斜率与截距；干净直线的斜率/截距、单个离群点不影响中位数斜率、点序无关性、退化输入被拒绝、FitLog10 的方向判定与倍增天数等既有行为不回归；定向测试、全量 go test ./... -count=1 与 go build ./... && go vet ./... 全部通过；校准与远端复跑均在 golang:1.22 linux/amd64 单架构完成。
