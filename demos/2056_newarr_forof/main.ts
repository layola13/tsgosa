function main(): i32 {
  let s: number = 0;
  for (const v of new Array(9, 8)) { s = s + v; }
  console.log(s);
  return 0;
}
