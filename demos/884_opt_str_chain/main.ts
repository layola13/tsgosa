function main(): i32 {
  const s: string|null = "hi";
  console.log(s?.length);
  console.log(s?.[0] ?? "x");
  return 0;
}
