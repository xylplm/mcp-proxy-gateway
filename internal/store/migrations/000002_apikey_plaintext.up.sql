-- API Key 明文存储（自部署场景，便于二次查看与复制）。
--
-- 背景：本网关为用户自部署，密钥仅存哈希会增加"丢失即不可恢复"的使用成本。
-- 经权衡，在自部署前提下允许存储明文以支持随时查看/复制；鉴权仍走 key_hash 等值查询，
-- 明文列不参与鉴权。安全提示：明文随库存储，请妥善保护数据库与加密备份。
ALTER TABLE api_key ADD COLUMN key_plain TEXT NOT NULL DEFAULT '';
