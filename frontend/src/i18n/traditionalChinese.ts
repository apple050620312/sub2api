import type { ConverterFunction } from 'opencc-js/core'

type MessageValue = string | MessageValue[] | { [key: string]: MessageValue }

let converterPromise: Promise<ConverterFunction> | undefined

async function getConverter(): Promise<ConverterFunction> {
  converterPromise ??= Promise.all([
    import('opencc-js/core'),
    import('opencc-js/dict/STCharacters'),
  ]).then(([{ ConverterFactory }, { default: characters }]) => ConverterFactory([characters]))

  return converterPromise
}

function convertValue(value: MessageValue, convert: ConverterFunction): MessageValue {
  if (typeof value === 'string') {
    return convert(value)
  }
  if (Array.isArray(value)) {
    return value.map((item) => convertValue(item, convert))
  }

  return Object.fromEntries(
    Object.entries(value).map(([key, item]) => [key, convertValue(item, convert)]),
  )
}

export async function toTraditionalChinese<T extends Record<string, unknown>>(messages: T): Promise<T> {
  const convert = await getConverter()
  return convertValue(messages as MessageValue, convert) as T
}
