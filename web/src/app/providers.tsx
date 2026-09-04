import {
  QueryClient,
  QueryClientProvider,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type PropsWithChildren,
} from "react";
import { api, setCsrfToken } from "../lib/api";
import type { Session } from "../types";

export type ThemePreference = "light" | "dark" | "system";

interface ThemeContextValue {
  preference: ThemePreference;
  resolved: "light" | "dark";
  setPreference: (preference: ThemePreference) => void;
  toggle: () => void;
}

const ThemeContext = createContext<ThemeContextValue | null>(null);
const AuthContext = createContext<{
  session?: Session;
  isPending: boolean;
  error: Error | null;
  refresh: () => Promise<void>;
  logout: () => Promise<void>;
} | null>(null);

function initialTheme(): ThemePreference {
  const stored = localStorage.getItem("appstore.theme");
  return stored === "light" || stored === "dark" || stored === "system"
    ? stored
    : "system";
}

function ThemeProvider({ children }: PropsWithChildren) {
  const [preference, setPreferenceState] =
    useState<ThemePreference>(initialTheme);
  const publicConfig = useQuery({
    queryKey: ["public-config"],
    queryFn: ({ signal }) => api.publicConfig(signal),
    staleTime: 60_000,
  });
  const [systemDark, setSystemDark] = useState(
    () => matchMedia("(prefers-color-scheme: dark)").matches,
  );
  const resolved =
    preference === "system" ? (systemDark ? "dark" : "light") : preference;

  useEffect(() => {
    if (localStorage.getItem("appstore.theme")) return;
    const configured = publicConfig.data?.theme;
    if (
      configured === "light" ||
      configured === "dark" ||
      configured === "system"
    ) {
      setPreferenceState(configured);
    }
  }, [publicConfig.data?.theme]);

  useEffect(() => {
    const query = matchMedia("(prefers-color-scheme: dark)");
    const listener = (event: MediaQueryListEvent) =>
      setSystemDark(event.matches);
    query.addEventListener("change", listener);
    return () => query.removeEventListener("change", listener);
  }, []);

  useEffect(() => {
    document.documentElement.dataset.theme = resolved;
    document.documentElement.style.colorScheme = resolved;
  }, [resolved]);

  const setPreference = useCallback((next: ThemePreference) => {
    localStorage.setItem("appstore.theme", next);
    setPreferenceState(next);
  }, []);

  const value = useMemo<ThemeContextValue>(
    () => ({
      preference,
      resolved,
      setPreference,
      toggle: () => setPreference(resolved === "dark" ? "light" : "dark"),
    }),
    [preference, resolved, setPreference],
  );

  return (
    <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
  );
}

export function AuthProvider({ children }: PropsWithChildren) {
  const queryClient = useQueryClient();
  const query = useQuery({
    queryKey: ["session"],
    queryFn: ({ signal }) => api.session(signal),
    retry: false,
    staleTime: 30_000,
  });

  useEffect(() => setCsrfToken(query.data?.csrfToken), [query.data?.csrfToken]);

  const value = useMemo(
    () => ({
      session: query.data,
      isPending: query.isPending,
      error: query.error,
      refresh: async () => {
        await queryClient.invalidateQueries({ queryKey: ["session"] });
      },
      logout: async () => {
        await api.logout();
        setCsrfToken();
        queryClient.setQueryData(["session"], {
          authenticated: false,
        } satisfies Session);
        await queryClient.invalidateQueries();
      },
    }),
    [query.data, query.error, query.isPending, queryClient],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: 1, refetchOnWindowFocus: false, staleTime: 15_000 },
    mutations: { retry: 0 },
  },
});

export function AppProviders({ children }: PropsWithChildren) {
  return (
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <AuthProvider>{children}</AuthProvider>
      </ThemeProvider>
    </QueryClientProvider>
  );
}

export function useTheme(): ThemeContextValue {
  const value = useContext(ThemeContext);
  if (!value) throw new Error("ThemeProvider가 필요합니다.");
  return value;
}

export function useAuth() {
  const value = useContext(AuthContext);
  if (!value) throw new Error("AuthProvider가 필요합니다.");
  return value;
}
