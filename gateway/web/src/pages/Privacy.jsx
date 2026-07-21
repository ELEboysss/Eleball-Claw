import { Shield, FileText } from 'lucide-react'

export default function Privacy() {
  return (
    <div className="flex-1 bg-eleball-bg pt-24 pb-16 px-4">
      <div className="max-w-3xl mx-auto">
        <div className="text-center mb-10">
          <div className="inline-flex items-center gap-2 px-4 py-2 rounded-full bg-eleball-primary-light text-eleball-primary-dark text-sm font-medium mb-4">
            <Shield className="w-4 h-4" />
            隐私政策
          </div>
          <h1 className="text-3xl font-bold text-eleball-text mb-2">Eleball 隐私政策</h1>
        </div>

        <div className="card p-8 space-y-8 text-sm text-eleball-text-secondary leading-relaxed">
          <section>
            <h2 className="text-lg font-semibold text-eleball-text mb-3 flex items-center gap-2">
              <FileText className="w-4 h-4 text-eleball-primary" />
              引言
            </h2>
            <p>
              Eleball（“我们”）重视用户的隐私保护。本隐私政策说明我们在你使用 Eleball 手机应用、Web
              端及相关服务（统称“本服务”）时，如何收集、使用、存储和保护你的个人信息。请你仔细阅读本政策。如你不同意本政策的任何内容，请停止使用本服务。
            </p>
          </section>

          <section>
            <h2 className="text-lg font-semibold text-eleball-text mb-3">一、我们收集的信息</h2>
            <p className="mb-2">为了向你提供服务，我们可能会收集以下类型的信息：</p>
            <ul className="list-disc pl-5 space-y-1.5">
              <li>
                <strong className="text-eleball-text">账户信息：</strong>
                用户名、昵称、密码哈希、注册设备标识等用于创建和维护账户的信息。
              </li>
              <li>
                <strong className="text-eleball-text">使用信息：</strong>
                你在使用本服务过程中产生的请求记录、Token 消耗量、余额变动、充值与兑换记录等。
              </li>
              <li>
                <strong className="text-eleball-text">设备与日志信息：</strong>
                设备型号、操作系统版本、应用版本、IP 地址、崩溃日志等，用于保障服务稳定性和安全。
              </li>
              <li>
                <strong className="text-eleball-text">你主动输入的内容：</strong>
                包括你在对话中输入的文本、截图、选中的屏幕内容等。此类内容主要用于向你返回 AI 响应。
              </li>
              <li>
                <strong className="text-eleball-text">第三方模型配置信息：</strong>
                当你选择使用自定义模型（BYOK）时，你需要自行提供第三方服务商的 API Key，该 Key 由你本地设备保管。
              </li>
            </ul>
          </section>

          <section>
            <h2 className="text-lg font-semibold text-eleball-text mb-3">二、信息的使用</h2>
            <p className="mb-2">我们使用收集的信息用于以下目的：</p>
            <ul className="list-disc pl-5 space-y-1.5">
              <li>提供、维护和改进本服务；</li>
              <li>完成 AI 模型调用并返回结果；</li>
              <li>进行计费、余额管理和交易记录；</li>
              <li>保障账户与服务安全，防止欺诈和滥用；</li>
              <li>向你发送服务通知、安全验证信息；</li>
              <li>遵守法律法规要求。</li>
            </ul>
          </section>

          <section>
            <h2 className="text-lg font-semibold text-eleball-text mb-3">三、信息的存储与保护</h2>
            <p>
              我们采取合理的技术和管理措施保护你的个人信息，防止未经授权的访问、泄露、篡改或丢失。
              你的账户密码以哈希方式存储，自定义模型所需的 API Key 由你的本地设备按平台最佳实践进行保护。
              我们会根据服务需要和法律法规要求，在必要期限内保留相关信息。
            </p>
          </section>

          <section>
            <h2 className="text-lg font-semibold text-eleball-text mb-3">四、信息的共享与披露</h2>
            <p className="mb-2">我们不会出售你的个人信息。在以下情形下，我们可能会披露你的信息：</p>
            <ul className="list-disc pl-5 space-y-1.5">
              <li>获得你的明确同意；</li>
              <li>为提供 AI 模型服务，向底层模型服务商传输必要的请求内容；</li>
              <li>根据法律法规、司法机关或行政机关的要求；</li>
              <li>为保护我们或他人的合法权益、财产安全或公共安全所必需。</li>
            </ul>
          </section>

          <section>
            <h2 className="text-lg font-semibold text-eleball-text mb-3">五、你的权利</h2>
            <p className="mb-2">你对你的个人信息享有以下权利：</p>
            <ul className="list-disc pl-5 space-y-1.5">
              <li>访问、更正你的账户信息；</li>
              <li>删除账户及相关数据（法律法规要求保留的除外）；</li>
              <li>撤回对某些处理的同意；</li>
              <li>通过服务内设置或联系我们行使上述权利。</li>
            </ul>
          </section>

          <section>
            <h2 className="text-lg font-semibold text-eleball-text mb-3">六、Cookie 与类似技术</h2>
            <p>
              Web 端可能使用 Cookie 或类似技术来保持登录状态、记录偏好设置和提升用户体验。
              你可以在浏览器设置中管理 Cookie。
            </p>
          </section>

          <section>
            <h2 className="text-lg font-semibold text-eleball-text mb-3">七、未成年人保护</h2>
            <p>
              本服务主要面向成年用户。如果你是未成年人，请在监护人陪同下使用，并在监护人同意本政策后使用相关服务。
            </p>
          </section>

          <section>
            <h2 className="text-lg font-semibold text-eleball-text mb-3">八、政策更新</h2>
            <p>
              我们可能会根据法律法规变化或服务调整不时修订本政策。修订后的政策将在本页面公布，公布后生效。
              如你继续使用本服务，即视为同意修订后的政策。
            </p>
          </section>

          <section>
            <h2 className="text-lg font-semibold text-eleball-text mb-3">九、联系我们</h2>
            <p>
              如你对本隐私政策有任何疑问，请通过以下方式联系我们：
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
