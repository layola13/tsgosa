function main(): i32 {
  const m = new Map<i32, i32>();
  m.set(1, 10);
  m.set(2, 20);
  console.log(m.keys()[0]);
  console.log(m.values()[1]);
  return 0;
}
