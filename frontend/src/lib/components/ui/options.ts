/**
 * 下拉类组件共用的候选项形状。
 *
 * Select 与 Combobox 走同一份字段名，调用方换控件时不必改数据形状；放在独立模块而不是
 * 挂在某个组件上，是为了让非组件模块也能 `import type`（时区候选就是这样用的）。
 */
export interface SelectOption {
  value: string;
  label: string;
  disabled?: boolean;
}
