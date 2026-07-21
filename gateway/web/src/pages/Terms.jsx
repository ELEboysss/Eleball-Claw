import { Scale, FileText } from 'lucide-react'

export default function Terms() {
  return (
    <div className="flex-1 bg-eleball-bg pt-24 pb-16 px-4">
      <div className="max-w-3xl mx-auto">
        <div className="text-center mb-10">
          <div className="inline-flex items-center gap-2 px-4 py-2 rounded-full bg-eleball-primary-light text-eleball-primary-dark text-sm font-medium mb-4">
            <Scale className="w-4 h-4" />
            服务条款
          </div>
          <h1 className="text-3xl font-bold text-eleball-text mb-2">Eleball 服务条款</h1>
        </div>

        <div className="card p-8 space-y-8 text-sm text-eleball-text-secondary leading-relaxed">
          <section>
            <h2 className="text-lg font-semibold text-eleball-text mb-3 flex items-center gap-2">
              <FileText className="w-4 h-4 text-eleball-primary" />
              协议确认
            </h2>
            <p>
              本服务条款（“本条款”）是你与 Eleball 运营方（“我们”）之间关于你使用 Eleball
              手机应用、Web 端及相关服务（统称“本服务”）的协议。
              在你开始注册、登录或使用本服务前，请仔细阅读本条款及
              <a href="/privacy" className="text-eleball-primary hover:underline mx-1">
                《隐私政策》
              </a>
              。一旦你点击同意或实际使用本服务，即表示你已阅读、理解并接受本条款及隐私政策的全部内容。
            </p>
          </section>

          <section>
            <h2 className="text-lg font-semibold text-eleball-text mb-3">一、账户管理</h2>
            <ul className="list-disc pl-5 space-y-1.5">
              <li>
                <strong className="text-eleball-text">注册资格：</strong>
                你应具备完全民事行为能力。如你未满十八周岁，请在监护人同意和陪同下使用本服务。
              </li>
              <li>
                <strong className="text-eleball-text">账户信息：</strong>
                你应提供真实、准确、完整的注册信息，并妥善保管账户密码。因你保管不善导致的任何损失由你自行承担。
              </li>
              <li>
                <strong className="text-eleball-text">账户安全：</strong>
                你对账户下的一切行为负责。如发现账户被盗用或存在安全风险，应立即通知我们。
              </li>
              <li>
                <strong className="text-eleball-text">账户限制：</strong>
                我们有权基于安全、合规或运营需要，对异常账户采取限制使用、暂停或注销等措施。
              </li>
            </ul>
          </section>

          <section>
            <h2 className="text-lg font-semibold text-eleball-text mb-3">二、服务内容与范围</h2>
            <p className="mb-2">
              Eleball 为用户提供便捷的 AI 助手能力，支持通过手机悬浮球、Web 端等方式发起对话，
              并可选配 Ele Agent 提供的模型或你自行接入的第三方模型（BYOK）。本服务包括但不限于：
            </p>
            <ul className="list-disc pl-5 space-y-1.5">
              <li>文本对话与上下文理解；</li>
              <li>基于屏幕截图、选中文本等上下文的多模态交互；</li>
              <li>账户充值、余额消费、兑换码（CDK）兑换等计费相关服务；</li>
              <li>开发者 API 调用能力。</li>
            </ul>
          </section>

          <section>
            <h2 className="text-lg font-semibold text-eleball-text mb-3">三、用户行为规范</h2>
            <p className="mb-2">你承诺在使用本服务时遵守法律法规，并不得从事以下行为：</p>
            <ul className="list-disc pl-5 space-y-1.5">
              <li>发布、传输或请求生成违法、侵权、淫秽、暴力、歧视、虚假或误导性内容；</li>
              <li>将本服务用于关键基础设施控制、医疗诊断、心理咨询等不适用的场景；</li>
              <li>未经授权访问、干扰或破坏本服务或相关系统；</li>
              <li>使用自动化脚本、爬虫等方式大量抓取或滥用本服务；</li>
              <li>转让、出借、共享账户或利用账户从事非法活动；</li>
              <li>侵犯他人知识产权、隐私权或其他合法权益。</li>
            </ul>
          </section>

          <section>
            <h2 className="text-lg font-semibold text-eleball-text mb-3">四、计费、充值与兑换</h2>
            <ul className="list-disc pl-5 space-y-1.5">
              <li>
                <strong className="text-eleball-text">计费方式：</strong>
                本服务可能按 Token 消耗量、调用次数或套餐包方式计费，具体以页面展示为准。
              </li>
              <li>
                <strong className="text-eleball-text">充值与兑换：</strong>
                你可以通过支持的渠道进行充值，或使用兑换码（CDK）兑换余额。兑换码一经使用通常不可退回。
              </li>
              <li>
                <strong className="text-eleball-text">余额使用：</strong>
                余额仅用于本服务消费，不可转让、提现或兑换为现金（法律法规另有规定的除外）。
              </li>
              <li>
                <strong className="text-eleball-text">发票：</strong>
                如你符合开具发票条件，可联系客服申请。
              </li>
            </ul>
          </section>

          <section>
            <h2 className="text-lg font-semibold text-eleball-text mb-3">五、自定义模型（BYOK）</h2>
            <p>
              当你选择使用自定义第三方模型时，你应自行负责获取该模型服务商的合法授权，并妥善保管你的 API Key。
              你理解并同意，你的 API Key 由你的本地设备或浏览器保管，我们对第三方模型的可用性、准确性、安全性及服务质量不承担责任。
            </p>
          </section>

          <section>
            <h2 className="text-lg font-semibold text-eleball-text mb-3">六、知识产权</h2>
            <ul className="list-disc pl-5 space-y-1.5">
              <li>
                本服务及相关软件、界面、标识、文档等知识产权归我们或相关权利人所有，未经许可不得复制、修改或反向工程。
              </li>
              <li>
                你输入的内容及其知识产权归你或原权利人所有。你同意授予我们为提供服务所必需的合理使用权。
              </li>
              <li>
                AI 生成的输出内容仅供参考，不构成专业意见。你应自行判断并承担使用风险。
              </li>
            </ul>
          </section>

          <section>
            <h2 className="text-lg font-semibold text-eleball-text mb-3">七、免责声明</h2>
            <ul className="list-disc pl-5 space-y-1.5">
              <li>
                本服务按“现状”和“可用性”提供。在法律允许的最大范围内，我们不就服务的连续性、准确性、完整性、安全性作出明示或默示保证。
              </li>
              <li>
                AI 模型可能存在“幻觉”、错误或不准确之处，你应对输出内容进行独立判断，我们不承担因此导致的任何损失。
              </li>
              <li>
                因网络、设备、第三方服务或不可抗力导致的服务中断或数据丢失，我们不承担责任，但会尽力协助恢复。
              </li>
            </ul>
          </section>

          <section>
            <h2 className="text-lg font-semibold text-eleball-text mb-3">八、责任限制</h2>
            <p>
              在法律允许的最大范围内，我们不对任何间接、附带、惩罚性或特殊损害承担责任，
              包括但不限于利润损失、数据丢失、商誉损失等。我们对单次或累计索赔所承担的责任不超过你在索赔发生前十二个月内就本服务实际支付的费用总额。
            </p>
          </section>

          <section>
            <h2 className="text-lg font-semibold text-eleball-text mb-3">九、协议终止</h2>
            <p>
              你可以随时停止使用本服务并申请注销账户。我们有权在你违反本条款、法律法规或危害本服务安全时，
              暂停或终止向你提供服务，且无需事先通知。协议终止后，本条款中关于知识产权、免责声明、责任限制等条款仍然有效。
            </p>
          </section>

          <section>
            <h2 className="text-lg font-semibold text-eleball-text mb-3">十、法律适用与争议解决</h2>
            <p>
              本条款的订立、执行和解释适用中华人民共和国法律。如双方就本条款产生争议，应首先友好协商解决；
              协商不成的，任何一方均可向本服务运营方所在地有管辖权的人民法院提起诉讼。
            </p>
          </section>

          <section>
            <h2 className="text-lg font-semibold text-eleball-text mb-3">十一、条款更新</h2>
            <p>
              我们可能根据法律法规变化或服务调整不时修订本条款。修订后的条款将在本页面公布，公布后生效。
              如你继续使用本服务，即视为同意修订后的条款；如不同意，请停止使用本服务。
            </p>
          </section>

          <section>
            <h2 className="text-lg font-semibold text-eleball-text mb-3">十二、联系我们</h2>
            <p>
              如你对本服务条款有任何疑问，请通过以下方式联系我们：
              <a href="mailto:support@eleball.cn" className="text-eleball-primary hover:underline ml-1">
                support@eleball.cn
              </a>
            </p>
          </section>
        </div>
      </div>
    </div>
  )
}
