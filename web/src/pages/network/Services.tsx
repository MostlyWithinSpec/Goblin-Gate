// Service objects (config "services"): protocol and ports.
import { ConfigList, inputClass, omitEmpty } from '../../components/ConfigList'
import type { Service } from '../../api/objects'
import { validateService } from './networkLogic'
import { t } from '../../i18n'

export default function Services() {
  return (
    <ConfigList<Service>
      title={t('services.title')}
      path="services"
      newLabel={t('services.newService')}
      select={(c) => c.services}
      empty={() => ({ name: '', protocol: 'tcp', ports: '' })}
      clean={(s) => omitEmpty({ ...s, ports: s.protocol === 'tcp' || s.protocol === 'udp' ? s.ports?.replace(/\s+/g, '') : '', comment: s.comment?.trim() })}
      validate={(s) => validateService(s)}
      columns={[
        { label: t('services.col.protocol'), render: (s) => s.protocol.toUpperCase() },
        { label: t('services.col.ports'), render: (s) => s.ports },
        { label: t('services.col.comment'), render: (s) => s.comment },
      ]}
      form={(s, set, { err }) => (
        <>
          <label className="block">
            {t('services.protocol')}
            <select aria-label={t('services.protocol')} className={inputClass} value={s.protocol} onChange={(e) => set({ protocol: e.target.value as Service['protocol'] })}>
              <option value="tcp">TCP</option>
              <option value="udp">UDP</option>
              <option value="icmp">ICMP</option>
              <option value="any">{t('services.protocol.any')}</option>
            </select>
          </label>
          {(s.protocol === 'tcp' || s.protocol === 'udp') && (
            <label className="block">
              {t('services.ports')}
              <input aria-label={t('services.ports')} className={inputClass} placeholder={t('services.portsPlaceholder')} value={s.ports ?? ''} onChange={(e) => set({ ports: e.target.value })} />
              {err('ports')}
            </label>
          )}
          <label className="block">
            {t('services.comment')}
            <input aria-label={t('services.comment')} className={inputClass} value={s.comment ?? ''} onChange={(e) => set({ comment: e.target.value })} />
          </label>
        </>
      )}
    />
  )
}
