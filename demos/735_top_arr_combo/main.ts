const C = [5, 6, 7] as const;
function main(): i32 {
  let s = 0;
  for (const x of C) {
    s = s + x;
  }
  console.log(s);
  console.log([...C.slice(1), 9][0]);
  return 0;
}
