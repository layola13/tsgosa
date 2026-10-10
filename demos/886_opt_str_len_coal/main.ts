function main(): i32 {
  const n: string|null = null;
  const s: string|null = "hi";
  console.log(n?.length ?? -1);
  console.log(s?.length ?? -1);
  return 0;
}
