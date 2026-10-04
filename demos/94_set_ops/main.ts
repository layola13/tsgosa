function probe(): i32 {
  const s = new Set();
  s.add(5);
  s.add(7);
  return s.has(5) + s.has(6);
}
function main(): i32 {
  console.log(probe());
  return 0;
}
