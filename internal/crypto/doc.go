// Package crypto 实现加密服务（Encryption_Service）：基于 AES-GCM 对上游鉴权
// 凭证加解密，密钥来自环境变量 MPG_ENCRYPTION_KEY；该变量留空时回退到内置默认
// 密钥并发出告警（仅适合本地/演示，生产环境务必显式配置，Req 19）。
//
// 核心类型为 Service：通过 New(key, logger) 构造，构造时完成密钥解码与有效性校验，
// 失败返回错误供启动流程记录并终止启动。Encrypt 对每条明文使用随机 nonce 并
// 将 nonce 前置到密文；Decrypt 为其可逆的逆操作。
//
// 密钥编码方案：MPG_ENCRYPTION_KEY 按「原始字节 → 十六进制 → base64」的优先级
// 解码，解码后须为 16/24/32 字节（推荐 32 字节用于 AES-256）。详见 crypto.go
// 中 decodeKey 的说明。
package crypto
