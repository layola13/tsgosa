function main(): i32 {
  const s: string = "hello world";
  const p = 6;
  console.log(s.startsWith("world", p) ? 1 : 0);
  console.log(s.endsWith("o w", p + 1) ? 1 : 0);
  console.log(s.indexOf("o", p));
  return 0;
}
