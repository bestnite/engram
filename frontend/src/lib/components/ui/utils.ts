import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

/**
 * cn 合并 Tailwind 类名：clsx 处理条件表达式，tailwind-merge 解决同类工具冲突
 * （后写者胜，例如外部传入的 `rounded-lg` 能覆盖组件默认的 `rounded-xl`）。
 *
 * 这是「多处复用的样式封装成组件」的地基：组件默认样式 + 调用方覆盖走同一条路径，
 * 页面里不再重复书写同一组类名（DESIGN.md §8）。
 */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
