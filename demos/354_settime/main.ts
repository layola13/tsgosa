function main(): i32 {
  const d = new Date();
  d.setTime(0);
  console.log(d.getTime());
  console.log(d.getFullYear());
  const e = new Date();
  d.setTime(e.getTime());
  console.log(d.getFullYear() - e.getFullYear());
  return 0;
}
