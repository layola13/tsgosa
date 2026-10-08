function main(): i32 {
  let s: string | null = null;
  const t: string = s ?? "dflt";
  console.log(t.length);
  return 0;
}
