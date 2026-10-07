function main(): i32 {
  const m = new Map<string, number[]>();
  const a: number[] = [1];
  m.set("k", a);
  console.log(m.get("k").length);
  const b: number[] = [1, 2];
  m.set("j", b);
  console.log(m.get("j")[1]);
  return 0;
}
