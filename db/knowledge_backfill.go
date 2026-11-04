package db

import (
	"fmt"
	"log"
)

// BackfillKnowledgeFromLegacy 把冻结的旧知识表回填进 knowledge_entries：
//   - company_knowledge → scope=company，entry_type=原 kind；
//   - attack_playbooks  → scope=global，entry_type=playbook。
//
// 幂等：靠 legacy_table/legacy_id 唯一索引 + ON CONFLICT DO NOTHING；旧表只读不改。
// 由 db.Open 在迁移锁内调用一次（失败只记日志，不阻塞启动）。
func (d *DB) BackfillKnowledgeFromLegacy() (companyRows, playbookRows int64, err error) {
	if d == nil || d.DB == nil {
		return 0, 0, fmt.Errorf("数据库未初始化")
	}
	res, err := d.Exec(`INSERT INTO knowledge_entries
		(scope, company_id, entry_type, key, title, summary, body_md, confidence,
		 source_task_id, source_node_id, status, legacy_table, legacy_id)
		SELECT 'company', k.company_id, k.kind, '', '', k.summary, COALESCE(k.detail,''),
		       COALESCE(k.confidence,''), COALESCE(k.source_task_id,''), COALESCE(k.source_node_id,0),
		       CASE WHEN k.status = 'active' THEN 'active' ELSE 'deprecated' END,
		       'company_knowledge', k.id
		FROM company_knowledge k
		WHERE k.kind IN ('fact','conclusion','asset_hint','playbook_hint')
		ON CONFLICT DO NOTHING`)
	if err != nil {
		return 0, 0, fmt.Errorf("回填 company_knowledge: %w", err)
	}
	companyRows, _ = res.RowsAffected()

	res, err = d.Exec(`INSERT INTO knowledge_entries
		(scope, entry_type, key, title, summary, body_md, fingerprint, fingerprint_key,
		 technique_key, confidence, source_task_id, source_node_id, status,
		 legacy_table, legacy_id)
		SELECT 'global', 'playbook', '', p.title, COALESCE(p.summary,''),
		       COALESCE(p.steps,'') || CASE WHEN COALESCE(p.evidence,'') <> ''
		            THEN E'\n\n' || p.evidence ELSE '' END,
		       p.fingerprint, p.fingerprint_key, p.technique_key, COALESCE(p.confidence,''),
		       COALESCE(p.source_task_id,''), COALESCE(p.source_node_id,0),
		       CASE WHEN p.status = 'active' THEN 'active' ELSE 'deprecated' END,
		       'attack_playbooks', p.id
		FROM attack_playbooks p
		WHERE COALESCE(p.fingerprint_key,'') <> '' AND COALESCE(p.technique_key,'') <> ''
		ON CONFLICT DO NOTHING`)
	if err != nil {
		return companyRows, 0, fmt.Errorf("回填 attack_playbooks: %w", err)
	}
	playbookRows, _ = res.RowsAffected()

	if companyRows+playbookRows > 0 {
		log.Printf("[knowledge] 旧表回填完成：company_knowledge %d 条、attack_playbooks %d 条", companyRows, playbookRows)
	}
	return companyRows, playbookRows, nil
}
