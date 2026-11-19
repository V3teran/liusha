import { useScenarioOptions } from './useScenarioQueries'

/** 按 code 显示场景名（定时模板引用的是活场景：显示当前名称；空 code = 无场景）。 */
export function ScenarioName({ code }: { code: string }) {
  const q = useScenarioOptions()
  if (!code) return <>无场景</>
  return <>{q.data?.find((s) => s.code === code)?.name ?? code}</>
}
