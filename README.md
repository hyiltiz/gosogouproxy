# gosogouproxy
搜狗浏览器加速代理

本程序将 Sogou 浏览器全网加速功能所使用的 HTTP 代理取出来，单独使用。

使用 Go 语言实现。

目前支持教育网、电信通、网通、联通的加速代理，支持 GET、POST、CONNECT 方法。

此程序的功能的可用性完全依赖于搜狗浏览器的代理协议保持不变，且服务器工作正常。 目前程序大致模拟搜狗浏览器 4.1.3.8107 的行为工作。

注意：搜狐浏览器已停止此代理服务，程序已失效。

未实现的功能：
* 从搜狗的服务器上取得代理列表。目前代理列表是直接写在程序里面，但抓包看的结果是代理列表是代理服务器地址列表可以另行取得，但似乎比较麻烦，没有实现。
* 使用 PAC 技术智能选择是否使用代理。搜狗服务器上似乎有混淆过的 PAC 数据，格式不明。 
