import type { Component } from 'svelte';

/**
 * 路由定义接口
 */
export interface RouteDefinition {
  path: string;
  name: string;
  component: Component<Record<string, unknown>>;
}

/**
 * 当前路由匹配信息
 */
export interface RouteMatch {
  path: string;
  params: Record<string, string>;
  query: Record<string, string>;
  /**
   * 原始查询串（不含前导 '?'，保留重复键）。query 是解析后的对象，重复键只留最后一个；
   * 需要「URL 就是视图身份」的判断（如按路由重建视图）时用这个原始值，别用 query。
   */
  search: string;
  route: RouteDefinition | null;
}
