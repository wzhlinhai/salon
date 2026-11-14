# 顾客小程序是独立主体，凭证与员工分离，按门店和微信 openid 绑定

顾客用微信登录小程序后，只应看到本人的档案、卡、消费明细和预约。顾客没有员工角色，也不能被授予员工权限。为把这条边界写在结构里，而不是靠每个处理函数记得过滤：

- **顾客是独立主体**：员工凭证里的 Subject 是员工 ID；顾客凭证用 `typ=customer`，Subject 是 `customer_logins.id`，另带租户和凭证版本，有效期 30 天。员工 `Authenticate` 拒绝带 `typ` 的凭证，顾客认证器拒绝员工凭证；同一个 JWT 密钥只用于签名，不共用凭证含义。
- **权限只有 CustomerSelf**：`ops.CustomerSelf` 只在 `Caller.CustomerID` 非空时为真。`ops.LoggedIn` 仍要求员工 ID，所以所有员工操作天然拒绝顾客。顾客操作声明为 Read、`CustomerSelf`、`AINone`，不向 AI 开放，也不接受 `customer_id` 参数。
- **绑定按门店和 openid**：`customer_logins` 以 `(store_id, openid)` 唯一，带租户和门店外键、行级安全。登录请求还不知道租户，用 SECURITY DEFINER 的 `customer_login_store(store_id)` 只按存在的门店取租户。
- **手机号自动关联档案**：首次登录用微信手机号在本店顾客里查找；找到就绑定，找不到就按现有顾客建档规则新建。新建顾客以顾客本人为操作者，`customers.created_by` 与 `created_by_customer` 恰好一个非空；审计日志同理。合并顾客时绑定跟随到保留的顾客。
- **每次请求重新解析**：认证器读取绑定、校验凭证版本，沿 `merged_into` 找到保留的顾客并更新绑定；绑定不存在或顾客不存在时视为未登录。数据范围始终是 `Caller.CustomerID` 加 `Caller.StoreID`，不信任请求参数。

## Considered Options

- **复用员工凭证，只加一个顾客角色**：未被采纳。员工登录要求密码和在职状态，顾客没有员工记录；把顾客塞进员工角色会污染权限目录，也会让所有员工操作少一道结构性拒绝。
- **登录后把 customer_id 放进请求参数**：未被采纳。任何漏改或参数注入都会让顾客读到别人的数据；范围只能来自认证后的 Caller。
- **按手机号全局绑定**：未被采纳。手机号唯一性按租户，不同门店/租户可能各有自己的顾客档案；绑定必须以门店为范围。

## Consequences

- 新增顾客可调用的操作时，必须用 `ops.CustomerSelf`，范围只取 `Caller.CustomerID` 和 `Caller.StoreID`。
- 顾客作为操作者的表必须同时保存员工/顾客两种操作者，并用 CHECK 保证恰好一个；读取操作者名称的地方要处理顾客操作者。
- 顾客合并必须同时改挂 `customer_logins`；认证器和登录路径都要跟随 `merged_into`，保证合并后仍是同一个人。
- 微信接口通过 `internal/wechat` 适配，生产配置 `WECHAT_APPID`/`WECHAT_SECRET`，开发用 `WECHAT_FAKE=1`；未配置时登录返回 503。
