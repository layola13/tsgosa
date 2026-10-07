function main(): i32 {
  const s: string = "abc";
  let r: string = "";
  for (const ch of s) { r = r + ch; console.log(ch); }
  console.log(r.length);
  return r.length;
}
console.log(main());
