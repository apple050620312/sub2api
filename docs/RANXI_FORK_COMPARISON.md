# ranxi2001 分支与原版的差异

比较时间：2026-10-20。原版取 `Wei-Shaw/sub2api` 的 `main`，提交 `8f2cbb53d9d1c40e1865b07ac460571f7d4c48de`；独立分支取 `ranxi2001/sub2api` 的 `production`，提交 `1aaf0e1b2576660151f0725b3a3b975264c7146a`。

它保留 Go、Vue、PostgreSQL、Redis 的基本架构，但已发展为面向中转站运维的独立产品线。主要区别是协议适配、账号质量检测、代理管理和调度控制，不能把它理解为只加几项功能的原版镜像。

| 方面 | 相对原版的主要变化 | 对本项目的意义 |
| --- | --- | --- |
| 维护与发布 | 默认生产分支为 `production`，按需同步上游，独立版本及容器发布 | 新版本号不等于包含原版所有最新修复 |
| Excel / BPS | 独立协议适配层、BPS OAuth、模型级路由、图片中转、工具历史与密文恢复、403 后探测恢复 | 扩大请求路径和状态复杂度，不能作为解决所有 Codex 过载的通用开关 |
| 账号质量运维 | 定时答题、随机推理题、模型判题、模型隔离、移出分组和自动恢复 | 适合需要自动运维的站点；检测会消耗请求额度，单题结果不代表模型整体质量 |
| 出口代理 | 内置 Mihomo，管理订阅、动态代理与静态节点绑定 | 大量出口可集中管理，但多出代理内核、端口和配置持久化的维护工作 |
| API Key 与调度 | Key 级并发等待队列、多分组 Key、上游分组倍率声明 | 与本项目按用户订阅计量和所属账号优先的策略有交叉，移植前需明确计费及回退规则 |
| 模型及协议兼容 | DeepSeek 的 Responses/Chat 转换、上下文与工具历史兼容，额外平台适配 | 对需要多平台的站点有价值；不等于上游保证模型质量或准入 |
| 故障诊断 | 请求阶段期限、共享首输出预算、传输回退诊断、密文恢复等修复 | 与“上游过载”问题最相关的是预算和诊断，可单独评估移植；无法增加上游实际容量 |
| 运维界面 | 质量运维、IP 管理、模型检测等多个独立管理区域 | 与本项目要求“自定义功能集中在服务中心”的界面方向不同 |

以上结论同时核对了 README、功能文档及对应源文件。例如 `account_quality.go`、`api_key_groups.go`、`basispoints/` 和 Mihomo 模块确实存在，未仅依据宣传文字。这里未部署或实测该分支，也未完成其全部安全审计。

## 代码规模与同步关系

在上述两个提交之间，Git 显示 2455 个文件存在差异，约增加 332652 行、删除 11857 行，包含测试、文档、生成代码和资源。两边历史已分叉：ranxi 侧有 1712 个原版历史中没有的提交，原版侧有 708 个 ranxi 历史中没有的提交。数字按提交祖先关系计算；等效修复可能已经被 cherry-pick 或重新实现，不能据此断言缺失 708 项修复。

原版近期仍有独立的 Gemini 计费、图片任务和支付退款修复；ranxi 近期则在更新 IP 风险控制、BPS 工具续轮、密文恢复和慢上传预算。比较时应按具体问题检查两边实现。

## 建议

当前八支账号共享、Discord 接入、按贡献分配订阅及滚动五小时额度的需求，继续维护现有分支更直接。优先借鉴请求耗时诊断、重试预算和可配置恢复策略；确有需求时再引入质量检测或 Mihomo。

不建议直接更换为 ranxi 镜像来修复过载。其数据库迁移、协议配置及请求状态机都有独立演进，应在隔离数据库中试迁移，并核对本项目定制功能、计费和回退行为。两个仓库的迁移编号及校验不能视为通用兼容接口。

## 来源

- [原版固定提交](https://github.com/Wei-Shaw/sub2api/tree/8f2cbb53d9d1c40e1865b07ac460571f7d4c48de)
- [ranxi 固定提交与 README](https://github.com/ranxi2001/sub2api/tree/1aaf0e1b2576660151f0725b3a3b975264c7146a)
- [质量运维文档](https://github.com/ranxi2001/sub2api/blob/1aaf0e1b2576660151f0725b3a3b975264c7146a/docs/account-quality-operations.md)
- [BPS 协议文档](https://github.com/ranxi2001/sub2api/blob/1aaf0e1b2576660151f0725b3a3b975264c7146a/docs/excel-bps.md)
- [Mihomo 静态节点文档](https://github.com/ranxi2001/sub2api/blob/1aaf0e1b2576660151f0725b3a3b975264c7146a/docs/mihomo-static-node-proxies.md)
- [多分组 Key 实现](https://github.com/ranxi2001/sub2api/blob/1aaf0e1b2576660151f0725b3a3b975264c7146a/backend/internal/service/api_key_groups.go)
- [分支比较页面](https://github.com/Wei-Shaw/sub2api/compare/main...ranxi2001:sub2api:production)
