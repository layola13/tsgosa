interface P { v: number; }
function main(): i32 {
  const d: P = { v: 3 };
  const n: P|null = null;
  const q: P = n ?? d;
  console.log(q.v);
  return 0;
}
