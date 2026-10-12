function main(): i32 {
  let f: boolean = true;
  const t: boolean = false;
  while ((f == t)) { f = true; break; }
  console.log(1);
  return 0;
}
