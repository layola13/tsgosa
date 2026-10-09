function main(): i32 {
  let s = "";
  for (const c of "ab") {
    s = c;
  }
  console.log(s);
  let t = "";
  for (const c of "ab") {
    t += c;
  }
  console.log(t);
  return 0;
}
