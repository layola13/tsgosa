function main(): i32 {
  const d: Date = new Date(1000);
  console.log(d.getTime());
  console.log(d.getFullYear());
  const e = new Date(2000);
  console.log(e.getTime());
  const n: Date = new Date();
  console.log(n.getTime() > 0 ? 1 : 0);
  return 0;
}
