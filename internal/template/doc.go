// Package template 实现模板市场（Template_Market）：维护内置分类化快捷模板集合，
// 提供按分类筛选、关键字检索（名称或简介命中）与模板详情查询，并为基于模板的上游
// 创建表单预填充提供数据（占位参数定义与预设连接参数）。
//
// 核心类型：Template（快捷模板）、Placeholder（占位参数定义）、Category（分类）、
// Market（模板市场，承载查询能力）。查询方法在无匹配或集合为空时返回空列表而非错误。
package template
