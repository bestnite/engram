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
  route: RouteDefinition | null;
}
