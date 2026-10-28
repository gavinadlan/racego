import type { Metadata } from "next";
import "@fontsource-variable/big-shoulders-display";
import "@fontsource-variable/instrument-sans";
import { Footer, Header } from "@/components/header";
import { SessionProvider } from "@/components/session";
import "./globals.css";

export const metadata: Metadata = {
  title: { default: "RaceGo: tiket race lari Indonesia", template: "%s | RaceGo" },
  description: "Kalender race lari Indonesia. Daftar fun run 5K sampai full marathon dari banyak penyelenggara, tiket QR langsung terbit.",
};

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html lang="id" className="h-full antialiased">
      <body className="flex min-h-full flex-col font-sans">
        <SessionProvider>
          <Header />
          <main className="mx-auto w-full max-w-6xl flex-1 px-4 py-10 sm:px-6">{children}</main>
          <Footer />
        </SessionProvider>
      </body>
    </html>
  );
}
