# 收款与硬件接入调研（issue #7）

调研日期：2026-10-18。范围：用现有微信支付商户号 + 扫码枪 + 小票打印机做前台收款；支付宝当面付作为可选项。

本文只采信一手来源：pay.weixin.qq.com、opendocs.alipay.com、developers.weixin.qq.com、官方 SDK 的 GitHub 仓库、硬件厂商官方文档、Chromium 源码、MDN。每条结论标注可信度：

- **【已核实】**：本次亲自打开官方页面或仓库读到原文。
- **【官方文档，未实测】**：官方文档这么写，但没有用真实商户号调用验证。
- **【推断】**：根据官方事实推出来的，文档没有直接写。
- **【未找到】**：在官方来源里没有找到，不能当事实用。

---

## 一句话结论

微信付款码支付目前**只有 V2 接口**（XML + MD5/HMAC-SHA256，撤销要双向证书），而官方 Go SDK `wechatpay-go` **只支持 APIv3**，覆盖不到付款码支付，所以付款码这块要自己写 V2 客户端，或者选社区库。回包里的 `openid` 是"用户在该 appid 下的唯一标识"，可以用来识别回头客，前提是始终用同一个 appid。支付宝当面付的回包有 `buyer_open_id`（同一个支付宝应用下唯一），但支付宝**没有官方 Go SDK**。扫码枪默认按 USB 键盘工作，只能配合前台 PC 网页用。小票打印有浏览器打印、本地代理、云打印三条路，选哪条取决于打印机型号，目前还不知道。

---

## 1. 微信付款码支付（商户被扫）

### 1.1 接口版本：只有 V2

- 【已核实】V3 商户文档中心左侧导航里，这个产品叫"付款码支付（V2）"，页面原文："付款码支付目前仍为V2接口，请按照付款码支付(V2)文档进行接入"（https://pay.weixin.qq.com/doc/v3/merchant/4012647301，更新于 2025.04.25；旧地址 https://pay.weixin.qq.com/docs/merchant/apis/code-payment-v3/direct/code-pay.html 也是同样的话）。
- 【已核实】V3 有付款码支付的只有两处，都和本项目无关：
  - 跨境版：`https://apihk.mch.weixin.qq.com/v3/global/micropay/transactions/pay`（https://pay.weixin.qq.com/doc/global/v3/zh/4013010527）。
  - "从业机构"文档里的付款码支付页（https://pay.weixin.qq.com/doc/v3/institution/4019825645），用的是 `sub_mch_id`、`channel_id`、SM2/RSA2 签名，面向收单机构。
  - 境内直连商户都不适用。
- 【未找到】第三方站点 wechatpay.js.org 列了 `/v3/pay/partner/transactions/codepay`（服务商模式）。官方文档里没找到这个接口，不采信。
- 【已核实】V3 的"查询订单"文档里，`trade_type` 枚举包含 `MICROPAY：付款码支付`；`trade_state` 里的 `REVOKED`、`USERPAYING`、`PAYERROR` 标注为"仅付款码支付会返回"（https://pay.weixin.qq.com/doc/v3/merchant/4012791899）。【推断】所以 V3 查单很可能也能查到付款码订单，但没实测。

### 1.2 V2 接口要点

【已核实】来源：付款码支付 API https://pay.weixin.qq.com/doc/v2/merchant/4011937125（更新于 2026.06.10）。

- **地址**：`POST https://api.mch.weixin.qq.com/pay/micropay`，备用域名 `api2.mch.weixin.qq.com`。这个接口不需要证书。
- **必填参数**：`appid`、`mch_id`、`nonce_str`、`sign`、`body`、`out_trade_no`、`total_fee`（单位：分）、`spbill_create_ip`、`auth_code`。
- **appid**：必须与 mchid 绑定。
- **auth_code**：文档写的是"18位纯数字，前缀为 10~15"。
- **协议规则**（https://pay.weixin.qq.com/doc/v2/merchant/4011986581）：
  - HTTPS + POST，XML 格式，根节点为 `xml`。
  - 签名算法 MD5/HMAC-SHA256，请求和应答都要验签。
  - "调用申请退款、撤销订单、红包接口等需要商户api证书"。
  - 判断顺序："先判断协议字段返回，再判断业务返回，最后判断交易状态"。
- **签名密钥**：用 APIv2 密钥，在商户平台"账户中心 → API安全 → 设置API密钥"里设置（https://pay.weixin.qq.com/wiki/doc/api/wxpay_v2/jiekouguize/chapter1_1.shtml）。它和 APIv3 密钥是两个不同的密钥。
- **成功判断**：`return_code` 和 `result_code` 都为 SUCCESS，并且 `trade_type` 为 MICROPAY。
- **结果未知时的处理流程**：
  - 遇到 `USERPAYING`（用户正在输密码）：等待 5 秒后查单，"间隔时间(建议10秒)重新查询……直到支付成功或超时(建议45秒)"。
  - 遇到 `SYSTEMERROR`、`BANKERROR`：立即查单。
  - 状态一直不明确：调用撤销订单。
- **开发指引**（https://pay.weixin.qq.com/doc/v2/merchant/4011936672）："当交易超时或支付交易失败，商户收银系统必须调用【撤销订单API】，撤销此交易"。
- **撤销订单**（https://pay.weixin.qq.com/doc/v2/merchant/4011937361）：
  - 地址 `https://api.mch.weixin.qq.com/secapi/pay/reverse`，"请求需要双向证书"。
  - 只能撤销 7 天以内的订单，`USERPAYING` 状态不能撤销。
  - 支付后至少等 15 秒再调。
  - 回包 `recall=Y` 表示需要重试。
- **免密规则**（https://pay.weixin.qq.com/doc/v2/merchant/4011936523）：
  - 金额 ≥1000 元的交易要验证密码。
  - 每个用户每天最多 10 笔免密。
  - 用户查看过付款码数字的，要验证密码。
  - 风控随时可能要求验密。
  - 结论：收银流程**必须**处理"用户支付中"这个状态。
- **预授权**：不支持，比如"押金冻结"模式（常见问题 https://pay.weixin.qq.com/doc/v2/merchant/4016429686）。

### 1.3 接入条件、商户类型、费率

- 【已核实】线下场所入驻指引（https://pay.weixin.qq.com/static/applyment_guide/applyment_detail_store.shtml）：
  - 分别列出了**个体工商户**和**企业**的申请材料。个体户的结算账户可以是"对公银行账户/法人对私账户"。
  - 线下场所入驻后，"可通过付款码支付或JSAPI支付"收款。
  - 结论：个体户和企业都能用普通商户号接入付款码支付，文档没写类型限制。
- 【已核实】费率。同一页原文："申请接入微信支付不收取任何申请费用"；交易手续费"一般与商户经营类目相关，0.6%至1%"。
  - 【未找到】美容美发类目的具体费率，官方公开页没有写。以商户平台上显示的签约费率为准。
- 【已核实】小微商户（无营业执照）只能由服务商通过 API 代为申请（https://pay.weixin.qq.com/core/affiliate/micro_intro）。如果老板的商户号是这类，或者是"微信收款商业版"这类免开发产品，能不能自己调 API 就有疑问，见文末开放问题。
- 【已核实 / 未找到】付款码支付权限：
  - 文档错误码 `NOAUTH`："商户没有开通被扫支付权限……请联系产品或商务申请"。说明这个权限不一定默认开通。
  - 【未找到】境内普通商户的"付款码支付开通指引"。跨境文档写的是"默认开通"，不能套用到境内。

### 1.4 openid 能否识别回头客

- 【已核实】付款码支付回包里的 `openid` 是必返字段，含义："用户在商户appid 下的唯一标识"。`is_subscribe` 已废弃，统一返回 N（4011937125）。
- 【已核实】常见问题（4016429686）："若两个接口使用相同appid调用，获取的openid是同一用户的唯一标识（由appid+用户身份生成），则结果一致。" 也就是说，付款码查到的 openid 和**同一 appid** 的小程序登录拿到的 openid 是同一个值。
- 结论：只要 appid 不换，同一个微信账号每次付款得到的 openid 都一样，可以作为"回头客"的稳定键。
- 【推断】这个识别有几个局限：
  - 换了 appid，openid 就变。
  - 顾客用另一个微信号付款，或者家人代付，会识别成另一个人。
  - 付款码回包里**没有** `unionid` 字段。【未找到】文档里没有付款码返回 unionid 的说法。

### 1.5 "付款码查询 openid"接口

【已核实】来源：https://pay.weixin.qq.com/doc/v2/merchant/4011940944（更新于 2025.07.07）。

- **地址**：`https://api.mch.weixin.qq.com/tools/authcodetoopenid`。
- **参数**：`appid`、`mch_id`、`auth_code`、`nonce_str`、`sign`。
- **作用**：不扣款，只拿 openid。
- **限制**：
  - 调用之后，"该付款码只能由此商户号发起扣款，直至付款码更新"。
  - 这个接口"仅用于获取openid"。如果后续要扣用户的资金、积分等资产，商户**必须做二次身份校验**（短信、会员密码等）。
  - 以 15、16 开头的付款码属于香港钱包，这个接口不支持（4016429686）。
- 【未找到】调用频率限制、需要单独申请的权限、商户类型限制，文档都没写。
- 【推断】用途：先扫码认出会员、展示储值余额或折扣，再决定走微信扣款还是扣储值。但如果扣储值，按官方要求要有二次校验，不能"扫一下就扣卡"。

---

## 2. 支付宝当面付（付款码 / 条码支付）

### 2.1 接入条件与费率

【已核实】来源：产品介绍 https://opendocs.alipay.com/open/194/105072（更新于 2026-04-14）。

- **账号类型**："支付宝企业账号、支付宝个人账号、个体工商户"。
- **签约材料**：
  - 经营场所照片（门头和店内）。
  - 店铺名称。
  - 实际经营地址。
  - "如以个人账号申请，需提供营业执照"。
- **材料不合格时**：收款额度受限，"单笔收款 ≤ 2000 元，单日收款 ≤ 20000 元"。缺的材料要在合约生效后 30 天内补全。
- **费率**：单笔 0.6%，各种支付工具（余额、银行卡、花呗等）同一费率。服务费四舍五入到分。退款时服务费一并退回。
- **结算**：
  - 默认实时到支付宝余额。
  - "新签约该产品未满 90 日或连续交易未满 30 日的商家"，资金次日结算。
- **退款期限**：默认 12 个月内可退。

【已核实】接入准备 https://opendocs.alipay.com/open/01csp3 和快速接入 https://opendocs.alipay.com/open/194/106039：

- 要在开放平台创建"网页/移动应用"，拿到 APPID。签名方式为 RSA2。
- 应用审核通过显示"已上线"，并且当面付开通后，才能调用接口。
- 收银系统"需配备红外扫描枪设备或其它的扫码设备"。
- 个人商家、单门店建议用"门店直连"方式。
- **付款码规则**：从 28 开头扩展到 **25–30 开头**，长度从 16–18 位扩展到 **16–24 位**。官方提醒不要写死码段和长度。

### 2.2 alipay.trade.pay 与顾客标识

【已核实】来源：https://opendocs.alipay.com/open/02ekfp（更新于 2026-08-17）。

- **请求**：`auth_code`（付款码）、`scene=bar_code`。
- **回包里的 `buyer_open_id`**："买家支付宝用户唯一标识"。
- **回包里的 `buyer_user_id`**：
  - 原文："新商户建议使用buyer_open_id替代该字段。对于新商户，buyer_user_id字段未来计划逐步回收"。
  - 要使用 openid，需要在"应用-开发配置-openid配置管理"里启用。
- **返回码**（快速接入页）：
  - 10000 只表示请求成功，最终状态以查询结果或异步通知为准。
  - 10003 表示等待用户付款，建议轮询查单。
  - 20000 表示未知异常，需要查单。
  - 40001–40006 表示失败。
  - 付款码支付没有同步跳转，只有异步通知。

【已核实】openid 的范围（https://opendocs.alipay.com/mini/0ai2i6）：

- openid 是"支付宝用户在应用维度下的唯一标识"。同一用户在不同应用下的 openid 不同。
- unionid 用于在同一商家账号的"应用分组"内跨应用识别。
- userid "后续将不再独立开放，以 openid 替换"。

结论：能识别回头客。键用 `buyer_open_id`，按支付宝 APPID 区分。同样要固定 APPID。

---

## 3. 扫码枪与网页、小程序的配合

- 【已核实】Zebra 官方文档说明 Keyboard Wedge 模式下，扫码枪"translates barcode data into keystrokes, which the host accepts as if they originated from the keyboard"（https://docs.zebra.com/us/en/scanners/general/sm72-ig/keyboard-wedge-interface.html）。
- 【已核实】Zebra Windows SDK 文档里的例子：HID 键盘模式下扫码，内容直接输入到 Notepad（https://techdocs.zebra.com/dcs/scanners/sdk-windows/）。
- 【官方文档，未实测】DS2208 等型号 USB 默认是 HID Keyboard，可以扫设置码加回车后缀（DS2208 快速指南 PDF，https://www.zebra.com/content/dam/support-dam/en/documentation/unrestricted/guide/product/ds2208-qsg-en.pdf）。本次没能抽取 PDF 文本逐字核对，以上来自 Zebra 官方站的检索摘要。老板的扫码枪品牌未知，各品牌设置码不同。
- **前台 PC 网页（Nuxt）**：【推断】
  - 扫码枪等同于键盘，收款页放一个获得焦点的输入框，收到回车就提交 `auth_code`，不需要驱动或浏览器权限。
  - 后端按前缀区分渠道：微信是 10–15 开头、18 位；支付宝是 25–30 开头、16–24 位。
  - 【推断，未见官方文档】要注意：中文输入法开着可能截获按键；焦点丢失会导致码输到别处。
- **员工小程序（uni-app）**：
  - USB 扫码枪接不了手机。
  - 【已核实】小程序可以用 `wx.scanCode` 调摄像头扫条码（`scanType` 含 `barCode`、`qrCode`），返回 `result`（https://developers.weixin.qq.com/miniprogram/dev/api/device/scan/wx.scanCode.html）。
  - 【未找到】官方没有说明用 `wx.scanCode` 扫顾客的微信付款码能不能正常拿到数字、合不合规，需要实测。
  - 【推断】结论：扫码收款主路径放在前台 PC；小程序只做订单和结账发起，或作为备用。

---

## 4. 小票打印机接入

| 方式 | 网页系统怎么打 | 依据 |
|---|---|---|
| USB / 驱动打印（浏览器打印） | 前台页面 `window.print()`，CSS 用 `@page { size: 80mm … }` 或 58mm 排版。Chrome 加启动参数 `--kiosk-printing` 可以自动点击打印预览里的"打印"，配合 `--use-system-default-printer` 选默认打印机 | 【已核实】Chromium 源码 `chrome/common/chrome_switches.h`："Enable automatically pressing the print button in print preview."（`kiosk-printing`）；【已核实】MDN `@page size`，Baseline 2024（https://developer.mozilla.org/en-US/docs/Web/CSS/@page/size） |
| USB 原始指令（WebUSB） | Chrome 用 WebUSB 直接发 ESC/POS 字节 | 【已核实】WebUSB 只能在 HTTPS 下用，处于实验阶段，不是 Baseline（MDN）；`requestDevice()` 必须由用户手势触发（https://developer.chrome.com/docs/capabilities/usb）。【未找到】Windows 下打印机被系统打印驱动占用时 WebUSB 能否 claim，官方没有写，风险高 |
| 本地打印代理 | PC 上跑一个小程序监听 localhost，网页把小票数据发给它，由它发 ESC/POS 给 USB 或网口打印机 | 【推断】是架构选项，不依赖某个厂商文档 |
| 网口 / Wi-Fi 打印机 | 浏览器不能直接开原始 TCP 连接。要么走系统驱动（同第一行），要么走本地代理 | 【未找到】常说的"9100 端口原始打印"本次没有找到厂商一手文档 |
| 云打印 | 后端 HTTP 调厂商 API，打印机走 Wi-Fi 或 4G 联网收单。前台 PC 和员工小程序都能触发打印 | 【已核实】飞鹅云 `POST https://api.de.feieyun.com/Api/Open/printMsg`，表单提交，`sig=SHA1(user+UKEY+stime)`，`content` ≤5000 字节，标签有 `<BR><CUT><CB><QR>` 等，先用 `Open_printerAddlist` 添加 SN+KEY，用 `Open_queryOrderState` 查打印状态；服务条款写"目前……免费"，日后收费提前 30 天通知（https://developer.de.feieyun.com/apidoc-cn.html）。只适用于飞鹅自家的云打印机 |

- 【官方文档，未能直接打开】Epson 的 ESC/POS 指令参考（https://download4.epson.biz/sec_pubs/pos/reference_en/escpos/index.html）本次访问返回 403。检索摘要显示：Epson 自称 ESC/POS 是其 "proprietary POS printer command system"，例如 `ESC @` 初始化、`GS V` 切纸、`GS ( k` 二维码。各品牌兼容程度要看具体型号的手册。
- 【推断】关键取决于老板现有打印机的接口和型号：
  - USB 普通热敏机：浏览器打印 + kiosk-printing 最省事。
  - 想精确控制切纸、开钱箱：上本地代理。
  - 飞鹅、易联云这类云打印机：直接走后端 API。易联云本次未调研。

---

## 5. Go SDK 覆盖情况

| 库 | 性质 | 协议 / 维护 | 是否覆盖本项目需要的接口 |
|---|---|---|---|
| `github.com/wechatpay-apiv3/wechatpay-go` | 微信支付官方 | 【已核实】Apache-2.0；最后提交 2025-07-07（v0.2.21）；README 标题"微信支付 API v3 Go SDK"，`services/payments` 下只有 app、h5、jsapi、native | **不覆盖付款码支付（V2）**。退款（`refunddomestic`）、V3 查单可以用。也可以用 `core.Client` 自己调 V3 接口 |
| 官方 V2 SDK | 微信支付官方 | 【已核实】"2022年9月22日起，'微信支付V2 SDK'将停止维护，不再提供新接入"（https://pay.weixin.qq.com/doc/v2/partner/4011985109）；历史版本只有 Java、.NET、PHP | 无 Go 版 |
| 官方 SDK 总体状态 | — | 【已核实】SDK 概述页（更新于 2026.04.24）："服务端 SDK 已停止更新维护，建议参考 WXPayUtility 工具类（Java / Go）实现接入"（https://pay.weixin.qq.com/doc/v3/merchant/4012076498）。Go 工具类页面（https://pay.weixin.qq.com/doc/v3/merchant/4015119334）只覆盖 V3 签名、验签、解密，不涉及 V2 的 MD5、HMAC 或 XML，也没有声明许可协议 | — |
| 支付宝官方服务端 SDK | 支付宝官方 | 【已核实】通用版只支持 Java、PHP、C#、Python、Node.js；原文"其它编程语言的 SDK（比如 GO 语言 SDK 等），支付宝官方暂未支持，您可以前往开源社区寻找开源版本"；同时推荐不能用 SDK 的开发者直接对接 OAS3.0 规范的 V3 OpenAPI（https://opendocs.alipay.com/common/02n6z6）。Easy SDK 只有 Java、C#、PHP | **无官方 Go SDK** |
| `github.com/alipay/global-open-sdk-go` | 蚂蚁官方 | 【已核实】MIT；README 标题 "Antom SDK for Go"，是跨境 Antom 产品 | 不适用于境内当面付 |
| `github.com/go-pay/gopay` | 社区 | 【已核实】Apache-2.0，最后推送 2026-08-22。源码有微信 V2 的 `Micropay`（`/pay/micropay`）、`AuthCodeToOpenId`（`/tools/authcodetoopenid`）、`Reverse`（`/secapi/pay/reverse`，支持证书）；支付宝有 `alipay.trade.pay`（另有 `alipay/v3` 包） | 覆盖本项目全部收款接口；非官方，需要自行审读 |
| `github.com/smartwalle/alipay` | 社区 | 【已核实】MIT，最后推送 2026-07-26，有 `TradePay()` | 只有支付宝 |

---

## 6. 对设计的影响

1. **收款适配层自己定义接口，每个渠道一个实现**：微信 V2 付款码、支付宝当面付、现金、储值各一个实现。微信 V3（退款、查单）可以用 wechatpay-go。V2 那部分要么自己写（XML + HMAC-SHA256 + 双向证书，工作量不大），要么引入 gopay 并锁定版本。
2. **收款单是一个状态机，不是一次同步调用**：状态包括待扫码、支付中（USERPAYING / 10003）、成功、失败、已撤销。后端负责轮询（微信建议每 10 秒查一次，45 秒超时）和撤销（微信要求支付后至少等 15 秒；支付宝用 cancel 或 close）。前台只展示状态。`out_trade_no` 必须幂等，重扫时换新单号。
3. **证书和密钥**：微信需要 APIv2 密钥（签名）、商户 API 证书（撤销和 V2 退款），用 V3 还需要 APIv3 密钥和平台公钥。都放在服务端，不进前端，也不进小程序。
4. **回头客识别**：会员表存"渠道 + appid + openid"三元组。微信付款码绑定的 appid 一旦选定就不要换。最好选将来顾客端小程序（如果有）会用的那个 appid，这样付款 openid 和小程序登录 openid 能对上。支付宝用 `buyer_open_id`，同样固定 APPID。
5. **"扫码认会员"再扣储值**：要按微信要求做二次校验（短信验证码或会员密码），不能只凭付款码 openid 直接扣储值。
6. **按付款码前缀路由渠道**：微信 10–15 开头、18 位；支付宝 25–30 开头、16–24 位，长度不要写死。
7. **扫码收款放在前台 PC 网页**：小程序端暂不做扫付款码。打印方案等确认打印机型号后再定，先按"浏览器打印 + kiosk-printing"设计小票模板（58/80mm），给本地代理或云打印留好接口。
8. **对账**：手续费按商户号实际签约费率记录（微信 0.6%–1% 看类目，支付宝 0.6%），不要写死在代码里。

## 7. 开放问题（需要老板确认或实测）

1. 老板的微信商户号是什么类型：普通直连商户（个体户还是企业）、小微商户，还是"收款商业版"这类？能不能登录商户平台设置 API 密钥、下载 API 证书？
2. 商户号的"付款码支付"权限有没有开通？绑定了哪些 appid（公众号、小程序）？签约费率是多少？
3. 有没有支付宝当面付？如果要开，需要门头照、店内照、营业执照，还要在开放平台创建应用并审核上线。
4. 扫码枪的品牌和型号。是否已经设置成 USB HID 键盘模式并带回车后缀？
5. 小票打印机的品牌、型号、接口（USB、网口、Wi-Fi、蓝牙、云），纸宽是 58mm 还是 80mm，有没有切刀和钱箱？前台 PC 用的系统和浏览器？
6. 需要实测：
   - V3 查单、V3 退款能否处理 V2 付款码订单（文档枚举暗示可以，但没明说）。
   - 小程序 `wx.scanCode` 能否读出微信付款码。
   - WebUSB 在前台 PC 上能否连上打印机。
7. 【未找到】美容美发类目在微信支付的具体费率；付款码支付和 authcodetoopenid 的调用频率限制。
