function main(): i32 {
  const s: string|null = "hi";
  const n: string|null = null;
  console.log(s?.length);
  console.log(n?.length);
  return 0;
}
