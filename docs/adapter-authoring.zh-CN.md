# 适配器编写

[English](adapter-authoring.md) | **中文**

适配器是在 `cmd/relaypulse/main.go` 中注册的编译期模块。它必须实现小巧的 `internal/adapter.Adapter` 契约，并返回经过校验的 `domain.Collection`；它从不直接写 SQLite。

接入新的站点家族时遵循以下流程：

1. 记录页面 URL 以及在 Chrome 中观察到的结构化请求。
2. 把脱敏后的响应样例保存到 `internal/adapter/testdata/`。
3. 为目录形状、精确原始名称、全部来源分组、空样本与详情序列编写契约测试。
4. 添加配置 schema 并注册适配器键名。
5. 在管理后台配置站点；不要把特定站点的凭据或代码路径加进采集器。

只要来源提供完整目录，适配器就应抓取完整目录。采集器会在详情请求之前先做热门模型匹配，因此大目录不会产生成比例的历史数据。零模型的目录属于错误，除非该目录本身有效且其中没有任何名称命中热门规则；这一区别会记录在采集运行中。

当来源暴露历史时间桶时，每次采集都应重新抓取一段有边界的重叠窗口（通常 24 小时），并返回窗口内每个真实时间桶。保持来源稳定的时间桶边界与分辨率：存储层按分组、开始时间和分辨率对重复时间桶做 upsert，从而修复错过的运行而不产生重复历史。绝不要从当前值、目录存在性或聚合值合成历史。零样本的不完整当前桶可以作为历史保存，但它不能取代最新有采样的当前状态。

适配器若希望公开的「过期样本隐藏卡片」策略评估某个模型，必须同时返回权威的 `HistoryCoverageStart` 与 `HistoryCoverageEnd`，覆盖至少连续 24 小时。当历史不可用、详情请求失败，或响应只能证明目录存在性/当前健康时，两个字段都不设置。`availabilityMode=presence` 与 `skipDetails=true` 刻意豁免于历史判定。覆盖是模型级的；详情部分成功时，只在成功的模型上声明。该策略从不删除模型或其存储的历史，后续真实样本一到，卡片自动恢复。

优先利用本来就要请求的目录或批量响应中内嵌的历史。当同一窗口在该响应中可得时，不要增加逐模型请求。若详情请求无法避免，单个模型失败后继续处理其余模型，并在成功的采集中附上有边界的类型化诊断。当所有已尝试的详情请求都失败时返回错误，让采集器保留上一个快照。混合结果会提交并记录为部分完成的运行。

当目录成员资格本身就是可用性信号时，把 `availabilityMode` 设为 `presence`。列出的模型为 healthy，而已知且未出现在完整目录中的模型被保留并标记为 failed。不要对分页或过滤后的响应使用此模式，除非适配器已确认拼装出了完整目录。

来源的原始名称与分组名称是身份数据。绝不为展示而规范化它们，也不要把规范化后的名字当作数据库身份键。

## 价格解码器

价格与健康采集相互独立。来源特定的价格格式应放在 `internal/pricing` 中，并实现 `pricing.Decoder` 契约。在 `pricing.DefaultRegistry` 注册；不要给存储层、采集器或公开面板添加针对特定来源的分支。

解码器接收定价响应和可选的状态/配置响应，返回规范化的模型元数据以及每个来源分组一条展示报价。规范化报价可以使用每百万 token 的输入/输出价格，也可以使用按次固定价。报价同时保留来源货币代码、货币符号、分组倍率以及来源应用的任何汇率换算。

当来源为每个渠道公布了完整计算好的价格时，解码器也可以返回直接的模型/分组报价。内置的 `model-market` 解码器读取 `data.items[].pricing`，把按 token 的数值归一为按百万 token 的数值，并把渠道名保留为来源分组。能用直接报价时，就不要重建来源已经算完的计费公式。

内置的 `newapi` 解码器读取：

- 比例计费的 `model_ratio`、`completion_ratio` 与 `group_ratio`；
- `cache_ratio`、`create_cache_ratio` 以及兼容的缓存创建别名；
- 缺少显式缓存倍率时，从 `billing_expr` 读取标准档位的 `cr` 与 `cc` 系数；
- 固定计费的 `model_price`；
- 状态响应中的 `quota_per_unit`、`quota_display_type`、`custom_currency_symbol` 与 `custom_currency_exchange_rate`。

注册新格式之前，先为每种计费模式、缺价状态、分组倍率和货币换算补充解码器测试。

探针适配器暴露三个可选配置字段：

```json
{
  "pricingAdapter": "newapi",
  "pricingPath": "/api/pricing",
  "pricingStatusPath": "/api/status"
}
```

当探针目录响应里已包含价格时，省略 `pricingPath`；配置的解码器会收到已抓取的目录。这避免了一次额外请求，适用于 `model-market` 载荷。

Uptime Kuma 适配器和探针适配器都可以挂接独立的价格源：

```json
{
  "statusBaseUrl": "https://status.example.com",
  "pricingBaseUrl": "https://api.example.com",
  "pricingAdapter": "newapi",
  "pricingPath": "/api/pricing",
  "pricingStatusPath": "/api/status",
  "pricingOptional": true,
  "pricingRequiresSession": true
}
```

`statusBaseUrl` 让健康采集保持在专门的状态源上。`pricingOptional` 隔离价格源故障，健康数据仍会更新。`pricingRequiresSession` 把价格源登录缺失暴露给会话同步。已存的 Cookie 与授权头只发送到站点 `baseUrl` 的精确源；跨域的状态请求始终使用公开抓取器。

对探针适配器而言，目录、批量状态与详情路径都基于 `statusBaseUrl` 解析；省略 `pricingBaseUrl` 时，价格路径落在站点自身的 `baseUrl` 上。当一个未分组的探针报告的是模型整体健康，而价格目录提供了具名分组时，适配器会把同一份健康观测投影到每个价格分组，而不是创建无关的 `default` 分组。

当探针页面与其价格端点使用已注册格式时，使用这些字段。若探针的价格载荷独一无二，则在 `internal/pricing` 下新增解码器、注册键名并在 `pricingAdapter` 中引用该键。凭据保存在加密会话存储中；绝不要把令牌放进适配器配置或测试样例。
