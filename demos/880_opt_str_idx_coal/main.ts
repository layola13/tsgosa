function main(): i32 {
  const n: string|null = null;
  const s: string|null = "hi";
  console.log(n?.[0] ?? "d");
  console.log(s?.[1] ?? "x");
  return 0;
}
