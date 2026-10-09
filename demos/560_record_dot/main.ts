function main(): i32 {
  const r: Record<string, i32> = { a: 1 };
  console.log(r.a);
  const s: Record<string, string> = { s: "hi" };
  console.log(s.s);
  return 0;
}
