# Dev App Store 

최근 1년 동안 개발된 73개의 오픈소스 프로젝트를 애플 앱스토어(App Store) 스타일의 반응형 웹 인터페이스로 탐색할 수 있는 인터랙티브 쇼케이스 허브입니다.

![App Store Preview](https://images.unsplash.com/photo-1618005182384-a83a8bd57fbe?q=80&w=1600&auto=format&fit=crop)

---

## ✨ 주요 기능

- **🌟 투데이 (Today View)**: 대표 플래그십 프로젝트(AgentHub, Miraq, vibe-coders) 및 에디터 추천, 인기 급상승 Top 8 랭킹
- **📱 전체 앱 카탈로그**: 73개 프로젝트 실시간 퍼지 검색 (`/`), 언어별 필터링, 정렬 (최근 업데이트순, 별점순, 이름순, 생성일순)
- **⚡ MCP (Model Context Protocol) 지원**: MCP 도구 및 프로토콜 연계 프로젝트(36개) 전용 뱃지 및 원클릭 필터링
- **📋 앱 상세 모달 (iOS Sheet)**: 터미널 실행 인터랙티브 프리뷰, 원클릭 `git clone` 복사, 핵심 기능 체크리스트, 기술 스택 뱃지
- **🌓 다크 / 라이트 모드**: 시스템 테마 자동 감지 및 사용자 설정 영속화 (`localStorage`)
- **💖 찜한 앱 (보관함)**: 관심 있는 프로젝트 즐겨찾기 북마크 관리

---

## 🛠️ 기술 스택

- **Frontend**: Vanilla HTML5, CSS3 (Modern Glassmorphism & Custom Properties), JavaScript (ES6+)
- **Architecture**: 외부 런타임 의존성이 없는 단일 독립형(Standalone) HTML 구조 (`index.html`)

---

## 🚀 시작하기

별도의 빌드나 설치 과정 없이 `index.html` 파일을 웹 브라우저에서 바로 실행할 수 있습니다.

```bash
# 저장소 클론
git clone https://github.com/hkjang/appstore.git

# 폴더 이동
cd appstore

# 브라우저로 열기 (macOS/Linux/Windows)
open index.html # macOS
xdg-open index.html # Linux
start index.html # Windows
```

---

## 📄 라이선스

This project is licensed under the MIT License.
