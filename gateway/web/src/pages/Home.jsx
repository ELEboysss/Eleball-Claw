import useSEO from '../hooks/useSEO'
import CloudFrame from '../components/CloudFrame'

// claw 官网（/）：内嵌云端 eleball.cn/，作为云端官网入口。
// 实时取云端，不维护本地副本（避免与云端内容漂移）；整页不跳转，URL 仍停留本地。
// 文档/隐私/条款等内容在官网 iframe 内导航到达，不再单独建本地路由。见 §C.2。
export default function Home() {
  useSEO('官网', 'Eleball 云端官网：产品介绍、文档、充值等。', false)
  return <CloudFrame path="/" iframeTitle="Eleball 官网" />
}
