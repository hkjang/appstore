const list = document.querySelector('#endpoints')
const status = document.querySelector('#status')
const filter = document.querySelector('#filter')
let operations = []

function render(query = '') {
  const needle = query.trim().toLocaleLowerCase('ko')
  const visible = operations.filter((item) =>
    `${item.method} ${item.path} ${item.summary} ${item.tags.join(' ')}`.toLocaleLowerCase('ko').includes(needle),
  )
  list.replaceChildren(...visible.map((item) => {
    const article = document.createElement('article')
    const heading = document.createElement('h2')
    const method = document.createElement('span')
    method.className = `method method-${item.method.toLowerCase()}`
    method.textContent = item.method
    const path = document.createElement('code')
    path.textContent = item.path
    heading.append(method, path)
    const summary = document.createElement('p')
    summary.textContent = item.summary || '설명 없음'
    const tags = document.createElement('small')
    tags.textContent = item.tags.join(' · ')
    article.append(heading, summary, tags)
    return article
  }))
  status.textContent = `${visible.length}개 endpoint`
}

fetch('/openapi.json', { headers: { Accept: 'application/json' } })
  .then((response) => {
    if (!response.ok) throw new Error(`HTTP ${response.status}`)
    return response.json()
  })
  .then((spec) => {
    operations = Object.entries(spec.paths).flatMap(([path, methods]) =>
      Object.entries(methods).filter(([method]) => ['get', 'post', 'put', 'patch', 'delete'].includes(method)).map(([method, operation]) => ({
        method: method.toUpperCase(), path, summary: operation.summary || '', tags: operation.tags || [],
      })),
    )
    render()
  })
  .catch(() => {
    status.textContent = 'API 명세를 불러오지 못했습니다.'
    status.classList.add('error')
  })

filter.addEventListener('input', () => render(filter.value))
