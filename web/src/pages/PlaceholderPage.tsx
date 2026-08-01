interface PlaceholderPageProps {
  title: string
}

export function PlaceholderPage({ title }: PlaceholderPageProps) {
  return (
    <div className="flex h-full items-center justify-center text-muted">
      <p className="text-sm">{title} · 建设中</p>
    </div>
  )
}
