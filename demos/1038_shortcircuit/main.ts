function main(): i32 {
  let t = 0;
  const f = () => { t += 1; return true; };
  const r1 = false && f();
  const r2 = true || f();
  console.log(t);
  console.log(r1 ? 1 : 0);
  console.log(r2 ? 1 : 0);
  return 0;
}
