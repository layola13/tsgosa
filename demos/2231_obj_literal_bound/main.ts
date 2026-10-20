export function main(): i32 {
  const a = [1, 2];
  const o = { xs: a };
  console.log(o.xs.length);
  const s = "hi";
  const p = { t: s };
  console.log(p.t.length);
  return 0;
}
