/**
 * 轻提示的唯一入口：视图从这里取 toast，不直接依赖 svelte-sonner，
 * 以后换实现只改这一处。挂载点见 App.svelte 里的 Toaster。
 */
export { toast } from 'svelte-sonner';
