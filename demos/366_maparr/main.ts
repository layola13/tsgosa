function main(): i32 {
  const m = new Map<string, number[]>();
  const a: number[] = [1];
  m.set("k", a);
  console.log(m.get("k").length);
  const b: number[] = [1, 2];
  m.set("j", b);
  console.log(m.get("j")[1]);
  const sm = new Map<string, string[]>();
  const sa: string[] = ["x", "yy"];
  sm.set("k", sa);
  console.log(sm.get("k")[1]);
  const sv = sm.get("k");
  console.log(sv[0].length);
  return 0;
}
