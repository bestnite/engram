/**
 * 题型元数据（GET /api/v1/card-types）的前端入口。
 *
 * 服务端的题型自描述是字段表与作答控件的唯一来源：新增题型只改 Go，前端不维护第二份
 * 清单。这里把响应包成一份目录，并暴露按 kind 取字段/正反面/作答控件的访问器。
 *
 * 加载是异步且幂等的：并发调用共用同一个 promise，成功后写入 store 并缓存；失败时清掉
 * inflight 以便重试，且不写入 store。目录为 null 时视图渲染加载/空态，绝不把 kind 机器词
 * 或裸语言包键当文案印出来。
 */
import { get, writable } from 'svelte/store';
import type { ApiClient } from './api/client';
import type {
  CardTypeAnswerControl,
  CardTypeDescription,
  CardTypesResponse,
} from './api/types';
import type { FieldSpec } from './card-fields';

export type { CardTypeDescription };

/** 题型自描述的本地视图：kinds 保持服务端顺序（kind 字典序），byKind 供 O(1) 查找。 */
export interface CardTypeCatalog {
  readonly kinds: readonly CardTypeDescription[];
  readonly byKind: ReadonlyMap<string, CardTypeDescription>;
}

/** 从一批自描述构造目录；重复 kind 以后者为准（服务端不会发重复 kind）。 */
export function createCatalog(kinds: readonly CardTypeDescription[]): CardTypeCatalog {
  const byKind = new Map<string, CardTypeDescription>();
  for (const description of kinds) {
    byKind.set(description.kind, description);
  }
  return { kinds: [...kinds], byKind };
}

/**
 * 题型元数据 store。null 表示尚未就绪（加载中或拉取失败）——视图据此渲染加载/空态。
 */
export const cardTypes = writable<CardTypeCatalog | null>(null);

let inflight: Promise<CardTypeCatalog> | null = null;

/**
 * 幂等加载题型自描述：并发调用共用同一个 promise，成功后写入 store 并缓存；
 * 失败时清掉 inflight 以便下次重试，且不写入 store（保持 null，让视图走降级路径）。
 */
export function loadCardTypes(client: ApiClient): Promise<CardTypeCatalog> {
  const cached = get(cardTypes);
  if (cached) return Promise.resolve(cached);
  if (!inflight) {
    inflight = client
      .getCardTypes()
      .then((response: CardTypesResponse) => {
        const catalog = createCatalog(response.kinds ?? []);
        cardTypes.set(catalog);
        return catalog;
      })
      .catch((cause) => {
        inflight = null;
        throw cause;
      });
  }
  return inflight;
}

/** 直接注入一份目录（测试或外壳预取时使用）；生产路径只走 loadCardTypes。 */
export function setCardTypes(catalog: CardTypeCatalog | null): void {
  inflight = null;
  cardTypes.set(catalog);
}

/** 当前快照；未就绪时返回 null。 */
export function currentCatalog(): CardTypeCatalog | null {
  return get(cardTypes);
}

/** 题型清单（下拉/筛选用），顺序与服务端一致。 */
export function kindOrder(catalog: CardTypeCatalog | null): string[] {
  return catalog ? catalog.kinds.map((description) => description.kind) : [];
}

/** 题型的自描述；未知题型或目录未就绪返回 null。 */
export function descriptionOf(
  catalog: CardTypeCatalog | null,
  kind: string | null | undefined
): CardTypeDescription | null {
  if (!catalog || !kind) return null;
  return catalog.byKind.get(kind) ?? null;
}

/**
 * 题型的字段表。服务端已把通用可选字段（source_url / extra）追加在每个题型的字段末尾，
 * 这里原样返回、不再追加；未知题型返回空表，视图退化为「无字段可编辑」。
 */
export function fieldsForKind(
  catalog: CardTypeCatalog | null,
  kind: string | null | undefined
): FieldSpec[] {
  const description = descriptionOf(catalog, kind);
  return description ? [...description.fields] : [];
}

/** 题型是否由服务端判分（等价于「题型实现了 Grader」）。 */
export function isGraded(
  catalog: CardTypeCatalog | null,
  kind: string | null | undefined
): boolean {
  return descriptionOf(catalog, kind)?.graded ?? false;
}

/** 正面字段名；未知题型返回空串。 */
export function frontField(
  catalog: CardTypeCatalog | null,
  kind: string | null | undefined
): string {
  return descriptionOf(catalog, kind)?.front_field ?? '';
}

/** 背面字段名；未知题型返回空串。 */
export function backField(
  catalog: CardTypeCatalog | null,
  kind: string | null | undefined
): string {
  return descriptionOf(catalog, kind)?.back_field ?? '';
}

/** 作答类题型的题面字段名；非作答题型返回空串。 */
export function promptField(
  catalog: CardTypeCatalog | null,
  kind: string | null | undefined
): string {
  return descriptionOf(catalog, kind)?.prompt_field ?? '';
}

/** 作答类题型的选项字段名；无选项字段返回空串。 */
export function optionsField(
  catalog: CardTypeCatalog | null,
  kind: string | null | undefined
): string {
  return descriptionOf(catalog, kind)?.options_field ?? '';
}

/** 作答控件类型；未知题型返回 none。 */
export function answerControl(
  catalog: CardTypeCatalog | null,
  kind: string | null | undefined
): CardTypeAnswerControl {
  return descriptionOf(catalog, kind)?.answer_control ?? 'none';
}
