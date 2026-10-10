function main(): i32 {
  const m = new Map<i32, i32>();
  m.set(1, 1);
  m.set(2, 2);
  console.log(m.size);
  m.delete(1);
  console.log(m.size);
  m.clear();
  console.log(m.size);
  return 0;
}
