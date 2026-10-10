function main(): i32 {
  const s: string = "hello";
  const p = 4;
  console.log(s.endsWith("hell", p) ? 1 : 0);
  console.log(s.endsWith("lo", p) ? 1 : 0);
  console.log(s.lastIndexOf("l", p));
  return 0;
}
