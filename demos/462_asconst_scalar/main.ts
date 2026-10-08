function main(): i32 {
  const s = "hi" as const;
  console.log(s);
  const x = 1 as const;
  console.log(x + 1);
  return 0;
}
