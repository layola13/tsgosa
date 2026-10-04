function probe(): i32 {
  const m = new Map();
  m.set("a", 1);
  m.clear();
  return m.getSize();
}
function main(): i32 {
  console.log(probe());
  return 0;
}