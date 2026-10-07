function main(): i32 {
  const s: string = "a1b2";
  const p = s.split(/1/);
  console.log(p.length);
  console.log(p[0]);
  console.log(p[1]);
  const q = s.split(/z/);
  console.log(q.length);
  console.log(q[0]);
  const r = "a1b2c3".split(/[0-9]/, 2);
  console.log(r.length);
  console.log(r[1]);
  return 0;
}
