import VisualTaskCard from './VisualTaskCard'

export default function VisualTaskList({ tasks, selectedTask, onSelect, onCancel, onDelete }) {
  if (tasks.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center py-8 text-eleball-vs-text-dim text-sm">
        <p>暂无生成任务</p>
        <p className="text-xs mt-1">生成的图片/视频将出现在这里</p>
      </div>
    )
  }

  return (
    <div className="space-y-3 overflow-y-auto pr-1">
      {tasks.map((task) => (
        <VisualTaskCard
          key={task.id}
          task={task}
          selected={selectedTask?.id === task.id}
          onSelect={onSelect}
          onCancel={onCancel}
          onDelete={onDelete}
        />
      ))}
    </div>
  )
}
