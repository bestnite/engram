import { writable } from 'svelte/store';

/**
 * 全站统一的二次确认：替代浏览器原生的 window.confirm。
 *
 * 原生确认框的样式、按钮文字由浏览器与系统语言决定，与应用的对话框不一致，也没有进出场动画。
 * 调用 askConfirm 得到一个 Promise：用户点确认为 true，取消或关闭为 false。
 * 同一时刻只有一个确认框；挂载点是 App.svelte 里的 ConfirmHost。
 */
export interface ConfirmOptions {
  title: string;
  description?: string;
  /** 确认按钮文字；缺省为「确定」。 */
  confirmLabel?: string;
  /** 危险操作（删除、撤销、禁用）：确认按钮用红色。 */
  destructive?: boolean;
}

export interface ConfirmRequest extends ConfirmOptions {
  resolve: (ok: boolean) => void;
}

export const confirmRequest = writable<ConfirmRequest | null>(null);

export function askConfirm(options: ConfirmOptions): Promise<boolean> {
  return new Promise((resolve) => {
    confirmRequest.update((pending) => {
      // 已有一个未答复的确认框时，先按取消结束它，避免旧的 Promise 永远挂起。
      pending?.resolve(false);
      return { ...options, resolve };
    });
  });
}
