import { HeroSection } from '../components/HeroSection'
import { FeaturesSection } from '../components/FeaturesSection'
import { HowItWorksSection } from '../components/HowItWorksSection'
import { TranslatorSection } from '../components/TranslatorSection'
import { CtaSection } from '../components/CtaSection'

export default function HomePage() {
  return (
    <main className="bg-[#080808]">
      <HeroSection />
      <FeaturesSection />
      <HowItWorksSection />
      <TranslatorSection />
      <CtaSection />
    </main>
  )
}
