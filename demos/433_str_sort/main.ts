function main(): i32 {
  const a = ["b", "a", "c"];
  a.sort();
  console.log(a[0]);
  console.log(a[2]);
  const b = ["y", "x"];
  const c = b.toSorted();
  console.log(c[0]);
  console.log(b[0]);
  return 0;
}
