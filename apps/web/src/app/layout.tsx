import "./globals.css";

export const metadata = {
  title: "ARUS",
  description: "Bot que compra y vende bitcoin entre casas de cambio para ganar con la diferencia de precio",
  icons: {
    icon: "/Logo-Arus.jpeg",
  },
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en" suppressHydrationWarning>
      <body suppressHydrationWarning>{children}</body>
    </html>
  );
}
