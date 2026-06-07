// Package backup 实现系统配置的导出与导入（Req 23.4、23.5、23.6）。
//
// 备份覆盖两部分：
//   - YAML 常规配置（config.YAMLConfig）；
//   - PG 业务配置（上游 MCP、别名规则、MCP/API Key 级屏蔽规则、API Key 元数据与来源白名单）。
//
// 设计要点：
//   - 备份的序列化、解析与校验为独立纯函数（Marshal/Unmarshal/Validate），
//     便于属性测试（任务 24.2，Property 27）在不依赖数据库的前提下验证「导出再
//     导入得到等价配置」与「非法备份被拒绝」。
//   - 业务配置的读取与应用抽象为 BusinessStore 接口，Service 在其之上编排导出/
//     导入流程；store 适配器（store_adapter.go）提供基于 PostgreSQL 仓储的实现。
//   - 导入时凡格式非法或内容校验失败，统一返回 domain.CodeBackupInvalid 错误（Req 23.6）。
package backup
